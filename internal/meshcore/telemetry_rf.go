package meshcore

import (
	"time"
)

// telemetryRFWindow is deliberately a small host-frame window, not a MeshCore
// protocol assertion. The pinned Companion writes LOG_RX synchronously before
// it parses an inbound packet and writes BINARY_RESPONSE for a matching tag.
// A delayed flood decode or busy stream must therefore become unavailable,
// rather than broadening this heuristic into a nearest-packet guess.
const telemetryRFWindow = 250 * time.Millisecond

const maxRecentRawRXCandidates = 32

// RFEvidence retains the result of the receiver-local, non-deterministic
// 0x88 -> 0x8C association. RSSI/SNR are set only for a selected association.
// NearestCandidate fields remain receiver-local ordering diagnostics and never
// turn this non-deterministic association into source or route proof.
type RFEvidence struct {
	Method                                 string   `json:"method"`
	Deterministic                          bool     `json:"deterministic"`
	Outcome                                string   `json:"outcome"`
	DeltaMS                                *int64   `json:"deltaMs,omitempty"`
	CandidateCount                         int      `json:"candidateCount"`
	FrameSequenceDelta                     *uint64  `json:"frameSequenceDelta,omitempty"`
	CandidateSequence                      *uint64  `json:"candidateSequence,omitempty"`
	CandidateRawSHA256                     string   `json:"candidateRawSha256,omitempty"`
	RSSI                                   *int     `json:"rssi,omitempty"`
	SNR                                    *float64 `json:"snr,omitempty"`
	AssociationConfidence                  string   `json:"associationConfidence,omitempty"`
	AssociationReason                      string   `json:"associationReason,omitempty"`
	NearestCandidateSequence               *uint64  `json:"nearestCandidateSequence,omitempty"`
	NearestCandidateDeltaMS                *int64   `json:"nearestCandidateDeltaMs,omitempty"`
	NearestCandidateFrameSequenceDelta     *uint64  `json:"nearestCandidateFrameSequenceDelta,omitempty"`
	NearestCandidateRSSI                   *int     `json:"nearestCandidateRssi,omitempty"`
	NearestCandidateSNR                    *float64 `json:"nearestCandidateSnr,omitempty"`
	NearestCandidateIsImmediatePredecessor *bool    `json:"nearestCandidateIsImmediatePredecessor,omitempty"`
}

type rawRXCandidate struct {
	observedAt time.Time
	sequence   uint64
	rssi       int
	snr        float64
	rawSHA256  string
}

func newRFEvidence(outcome string, candidateCount int) RFEvidence {
	return RFEvidence{
		Method: "ordered_temporal", Deterministic: false, Outcome: outcome, CandidateCount: candidateCount,
	}
}

func (a *Adapter) recordRawRXCandidate(frame []byte, observedAt time.Time, sequence uint64) {
	if len(frame) < 3 || frame[0] != PushLogRXData || sequence == 0 {
		return
	}
	candidate := rawRXCandidate{
		observedAt: observedAt.UTC(), sequence: sequence, rssi: int(int8(frame[2])),
		snr: float64(int(int8(frame[1]))) / 4, rawSHA256: sha256Hex(frame),
	}
	a.telemetryMu.Lock()
	defer a.telemetryMu.Unlock()
	a.recentRawRX = append(a.recentRawRX, candidate)
	if len(a.recentRawRX) > maxRecentRawRXCandidates {
		a.recentRawRX = append([]rawRXCandidate(nil), a.recentRawRX[len(a.recentRawRX)-maxRecentRawRXCandidates:]...)
	}
}

// correlateTaggedTelemetryRF selects the latest temporally eligible raw
// candidate only after the 0x8C tag has matched an active, non-expired
// request. Ordering controls confidence, not identity or response-route
// attribution. It must run while telemetryMu is held.
func (a *Adapter) correlateTaggedTelemetryRF(request *telemetryRequest, responseAt time.Time, responseSequence uint64) RFEvidence {
	if request == nil || request.requestedAt.IsZero() || responseSequence == 0 {
		return newRFEvidence("no_candidate", 0)
	}

	var candidates []rawRXCandidate
	var eligible []rawRXCandidate
	var beforeRequest *rawRXCandidate
	for index := range a.recentRawRX {
		candidate := a.recentRawRX[index]
		if candidate.sequence >= responseSequence || candidate.observedAt.After(responseAt) {
			continue
		}
		if candidate.sequence <= request.requestFrameSequence || candidate.observedAt.Before(request.requestedAt) {
			copy := candidate
			beforeRequest = &copy
			continue
		}
		candidates = append(candidates, candidate)
		delta := responseAt.UTC().Sub(candidate.observedAt)
		if delta >= 0 && delta <= a.currentTelemetryRFWindowLocked() {
			eligible = append(eligible, candidate)
		}
	}
	if len(eligible) == 0 {
		if len(candidates) > 0 {
			evidence := newRFEvidence("outside_window", len(candidates))
			setNearestCandidateDiagnostic(&evidence, nearestRawRXCandidate(candidates), responseAt, responseSequence)
			return evidence
		}
		if beforeRequest != nil {
			return newRFEvidence("candidate_before_request_start", 0)
		}
		return newRFEvidence("no_candidate", 0)
	}
	candidate := nearestRawRXCandidate(eligible)
	evidence := newRFEvidence("matched_ordered_temporal", len(candidates))
	setNearestCandidateDiagnostic(&evidence, candidate, responseAt, responseSequence)
	evidence.CandidateRawSHA256 = candidate.rawSHA256
	evidence.CandidateSequence = uint64Ptr(candidate.sequence)
	sequenceDelta := responseSequence - candidate.sequence
	evidence.FrameSequenceDelta = uint64Ptr(sequenceDelta)
	delta := responseAt.UTC().Sub(candidate.observedAt)
	if delta >= 0 {
		deltaMS := delta.Milliseconds()
		evidence.DeltaMS = &deltaMS
	}
	evidence.AssociationConfidence, evidence.AssociationReason = telemetryRFAssociationConfidence(sequenceDelta)
	if _, consumed := a.consumedRawRX[candidate.sequence]; consumed {
		evidence.Outcome = "candidate_already_consumed"
		evidence.AssociationConfidence, evidence.AssociationReason = "", ""
		return evidence
	}
	a.consumedRawRX[candidate.sequence] = struct{}{}
	evidence.Outcome = "matched_ordered_temporal"
	evidence.RSSI = intPtr(candidate.rssi)
	evidence.SNR = rfFloat64Ptr(candidate.snr)
	return evidence
}

func telemetryRFAssociationConfidence(sequenceDelta uint64) (string, string) {
	switch sequenceDelta {
	case 1:
		return "high", "immediate_predecessor"
	case 2:
		return "medium", "one_intervening_frame"
	default:
		return "low", "nearest_preceding_candidate"
	}
}

func nearestRawRXCandidate(candidates []rawRXCandidate) rawRXCandidate {
	nearest := candidates[0]
	for _, candidate := range candidates[1:] {
		// All eligible candidates precede the response. The highest frame
		// sequence is therefore the one nearest in the ordered host stream.
		if candidate.sequence > nearest.sequence {
			nearest = candidate
		}
	}
	return nearest
}

// setNearestCandidateDiagnostic exposes local ordering observability. It uses
// distinct fields from selected RSSI/SNR so diagnostics never imply that a
// candidate is deterministic source or route evidence.
func setNearestCandidateDiagnostic(evidence *RFEvidence, candidate rawRXCandidate, responseAt time.Time, responseSequence uint64) {
	if evidence == nil || candidate.sequence == 0 || responseSequence <= candidate.sequence {
		return
	}
	sequenceDelta := responseSequence - candidate.sequence
	evidence.NearestCandidateSequence = uint64Ptr(candidate.sequence)
	evidence.NearestCandidateFrameSequenceDelta = uint64Ptr(sequenceDelta)
	evidence.NearestCandidateIsImmediatePredecessor = boolPtr(sequenceDelta == 1)
	evidence.NearestCandidateRSSI = intPtr(candidate.rssi)
	evidence.NearestCandidateSNR = rfFloat64Ptr(candidate.snr)
	if delta := responseAt.UTC().Sub(candidate.observedAt); delta >= 0 {
		evidence.NearestCandidateDeltaMS = int64Ptr(delta.Milliseconds())
	}
}

func (a *Adapter) currentTelemetryRFWindowLocked() time.Duration {
	if a.telemetryRFWindow > 0 {
		return a.telemetryRFWindow
	}
	return telemetryRFWindow
}

func intPtr(value int) *int { return &value }

func int64Ptr(value int64) *int64 { return &value }

func uint64Ptr(value uint64) *uint64 { return &value }

func rfFloat64Ptr(value float64) *float64 { return &value }

func boolPtr(value bool) *bool { return &value }
