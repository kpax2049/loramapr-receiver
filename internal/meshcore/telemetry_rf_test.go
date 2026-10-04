package meshcore

import (
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/receiverevents"
)

func TestTaggedTelemetryRFCorrelationSelectsLatestTemporallyEligibleCandidate(t *testing.T) {
	start := time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)
	newAdapter := func() *Adapter {
		adapter := NewAdapter(Config{}, nil, nil)
		adapter.telemetryRFWindow = telemetryRFWindow
		return adapter
	}
	request := func() *telemetryRequest {
		return &telemetryRequest{requestedAt: start, requestFrameSequence: 5}
	}
	record := func(adapter *Adapter, at time.Time, sequence uint64, rssi int, snrX4 int8) {
		adapter.recordRawRXCandidate([]byte{PushLogRXData, byte(snrX4), byte(int8(rssi)), 1, 2, 3}, at, sequence)
	}
	evaluate := func(adapter *Adapter, value *telemetryRequest, at time.Time, sequence uint64) RFEvidence {
		adapter.telemetryMu.Lock()
		defer adapter.telemetryMu.Unlock()
		return adapter.correlateTaggedTelemetryRF(value, at, sequence)
	}

	tests := []struct {
		name             string
		prepare          func(*Adapter)
		responseAt       time.Time
		responseSeq      uint64
		wantOutcome      string
		wantCandidates   int
		wantAccepted     bool
		confidence       string
		reason           string
		wantRSSI         int
		wantSNR          float64
		wantNearest      bool
		nearestSeq       uint64
		nearestDelta     int64
		nearestFrame     uint64
		nearestImmediate bool
	}{
		{
			name:       "exactly one ordered candidate is accepted",
			prepare:    func(adapter *Adapter) { record(adapter, start.Add(10*time.Millisecond), 10, -77, 10) },
			responseAt: start.Add(11 * time.Millisecond), responseSeq: 11,
			wantOutcome: "matched_ordered_temporal", wantCandidates: 1,
			wantAccepted: true, confidence: "high", reason: "immediate_predecessor", wantRSSI: -77, wantSNR: 2.5,
			wantNearest: true, nearestSeq: 10, nearestDelta: 1, nearestFrame: 1, nearestImmediate: true,
		},
		{
			name:    "no raw log leaves RF unavailable",
			prepare: func(*Adapter) {}, responseAt: start.Add(11 * time.Millisecond), responseSeq: 11,
			wantOutcome: "no_candidate", wantCandidates: 0,
		},
		{
			name: "three candidates with immediate latest candidate are high confidence",
			prepare: func(adapter *Adapter) {
				record(adapter, start.Add(5*time.Millisecond), 8, -91, 4)
				record(adapter, start.Add(8*time.Millisecond), 9, -83, 8)
				record(adapter, start.Add(10*time.Millisecond), 10, -70, 12)
			},
			responseAt: start.Add(11 * time.Millisecond), responseSeq: 11,
			wantOutcome: "matched_ordered_temporal", wantCandidates: 3,
			wantAccepted: true, confidence: "high", reason: "immediate_predecessor", wantRSSI: -70, wantSNR: 3,
			wantNearest: true, nearestSeq: 10, nearestDelta: 1, nearestFrame: 1, nearestImmediate: true,
		},
		{
			name: "three candidates with one intervening frame are medium confidence",
			prepare: func(adapter *Adapter) {
				record(adapter, start.Add(5*time.Millisecond), 7, -91, 4)
				record(adapter, start.Add(10*time.Millisecond), 8, -83, 8)
				record(adapter, start.Add(10*time.Millisecond), 10, -70, 12)
			},
			responseAt: start.Add(39 * time.Millisecond), responseSeq: 12,
			wantOutcome: "matched_ordered_temporal", wantCandidates: 3,
			wantAccepted: true, confidence: "medium", reason: "one_intervening_frame", wantRSSI: -70, wantSNR: 3,
			wantNearest: true, nearestSeq: 10, nearestDelta: 29, nearestFrame: 2, nearestImmediate: false,
		},
		{
			name:       "one candidate with one intervening frame is medium confidence",
			prepare:    func(adapter *Adapter) { record(adapter, start.Add(10*time.Millisecond), 10, -77, 10) },
			responseAt: start.Add(39 * time.Millisecond), responseSeq: 12,
			wantOutcome: "matched_ordered_temporal", wantCandidates: 1,
			wantAccepted: true, confidence: "medium", reason: "one_intervening_frame", wantRSSI: -77, wantSNR: 2.5,
			wantNearest: true, nearestSeq: 10, nearestDelta: 29, nearestFrame: 2, nearestImmediate: false,
		},
		{
			name:       "candidate beyond one intervening frame is low confidence",
			prepare:    func(adapter *Adapter) { record(adapter, start.Add(10*time.Millisecond), 9, -77, 10) },
			responseAt: start.Add(39 * time.Millisecond), responseSeq: 13,
			wantOutcome: "matched_ordered_temporal", wantCandidates: 1,
			wantAccepted: true, confidence: "low", reason: "nearest_preceding_candidate", wantRSSI: -77, wantSNR: 2.5,
			wantNearest: true, nearestSeq: 9, nearestDelta: 29, nearestFrame: 4, nearestImmediate: false,
		},
		{
			name:       "candidate outside bounded one way window is unavailable",
			prepare:    func(adapter *Adapter) { record(adapter, start.Add(time.Millisecond), 10, -77, 10) },
			responseAt: start.Add(telemetryRFWindow + 2*time.Millisecond), responseSeq: 11,
			wantOutcome: "outside_window", wantCandidates: 1,
			wantRSSI: -77, wantSNR: 2.5,
			wantNearest: true, nearestSeq: 10, nearestDelta: int64(telemetryRFWindow/time.Millisecond + 1), nearestFrame: 1, nearestImmediate: true,
		},
		{
			name:       "candidate before request start is rejected",
			prepare:    func(adapter *Adapter) { record(adapter, start.Add(-time.Millisecond), 10, -77, 10) },
			responseAt: start.Add(time.Millisecond), responseSeq: 11,
			wantOutcome: "candidate_before_request_start", wantCandidates: 0,
		},
		{
			name: "latest candidate wins over older candidate closer to request start",
			prepare: func(adapter *Adapter) {
				record(adapter, start.Add(time.Millisecond), 7, -91, 4)
				record(adapter, start.Add(28*time.Millisecond), 10, -70, 12)
			},
			responseAt: start.Add(29 * time.Millisecond), responseSeq: 11,
			wantOutcome: "matched_ordered_temporal", wantCandidates: 2,
			wantAccepted: true, confidence: "high", reason: "immediate_predecessor", wantRSSI: -70, wantSNR: 3,
			wantNearest: true, nearestSeq: 10, nearestDelta: 1, nearestFrame: 1, nearestImmediate: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := newAdapter()
			test.prepare(adapter)
			evidence := evaluate(adapter, request(), test.responseAt, test.responseSeq)
			if evidence.Method != "ordered_temporal" || evidence.Deterministic || evidence.Outcome != test.wantOutcome || evidence.CandidateCount != test.wantCandidates {
				t.Fatalf("evidence = %#v", evidence)
			}
			if test.wantAccepted {
				if evidence.RSSI == nil || *evidence.RSSI != test.wantRSSI || evidence.SNR == nil || *evidence.SNR != test.wantSNR || evidence.AssociationConfidence != test.confidence || evidence.AssociationReason != test.reason {
					t.Fatalf("accepted evidence lost selected RF/confidence: %#v", evidence)
				}
			} else if evidence.RSSI != nil || evidence.SNR != nil || evidence.AssociationConfidence != "" || evidence.AssociationReason != "" {
				t.Fatalf("rejected evidence must not expose RF values: %#v", evidence)
			}
			if test.wantNearest {
				if evidence.NearestCandidateSequence == nil || *evidence.NearestCandidateSequence != test.nearestSeq ||
					evidence.NearestCandidateDeltaMS == nil || *evidence.NearestCandidateDeltaMS != test.nearestDelta ||
					evidence.NearestCandidateFrameSequenceDelta == nil || *evidence.NearestCandidateFrameSequenceDelta != test.nearestFrame ||
					evidence.NearestCandidateIsImmediatePredecessor == nil || *evidence.NearestCandidateIsImmediatePredecessor != test.nearestImmediate ||
					evidence.NearestCandidateRSSI == nil || *evidence.NearestCandidateRSSI != test.wantRSSI ||
					evidence.NearestCandidateSNR == nil || *evidence.NearestCandidateSNR != test.wantSNR {
					t.Fatalf("nearest candidate diagnostic = %#v", evidence)
				}
			}
		})
	}
}

func TestTaggedTelemetryRFCorrelationDoesNotReuseConsumedCandidate(t *testing.T) {
	start := time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)
	adapter := NewAdapter(Config{}, nil, nil)
	adapter.recordRawRXCandidate([]byte{PushLogRXData, 8, ^byte(79), 1}, start.Add(time.Millisecond), 10)
	evaluate := func() RFEvidence {
		adapter.telemetryMu.Lock()
		defer adapter.telemetryMu.Unlock()
		return adapter.correlateTaggedTelemetryRF(&telemetryRequest{requestedAt: start, requestFrameSequence: 5}, start.Add(2*time.Millisecond), 11)
	}
	if got := evaluate(); got.Outcome != "matched_ordered_temporal" {
		t.Fatalf("first association = %#v", got)
	}
	if got := evaluate(); got.Outcome != "candidate_already_consumed" || got.RSSI != nil || got.SNR != nil {
		t.Fatalf("consumed candidate was reused: %#v", got)
	}
}

func TestNormalizeTaggedTelemetryRFEvidenceUsesGenericRadioMetrics(t *testing.T) {
	key := trackingKey
	tag := uint32(0x66554433)
	voltage := 4.09
	delta := int64(1)
	sequence := uint64(42)
	sequenceDelta := uint64(1)
	rssi := -77
	snr := 2.5
	normalized, err := NormalizeTelemetryResult(TelemetryResult{
		TargetPublicKey: key, SourcePrefix: key[:12], RequestTag: &tag, Tagged: true, ResponseOpcode: PushBinaryResponse,
		ReceivedAt: startOfRFFixture(), RawFrame: []byte{PushBinaryResponse, 0, 0x33, 0x44, 0x55, 0x66, 1, 116, 1, 0x99}, Telemetry: Telemetry{Voltage: &voltage},
		RFEvidence: RFEvidence{Method: "ordered_temporal", Deterministic: false, Outcome: "matched_ordered_temporal", AssociationConfidence: "high", AssociationReason: "immediate_predecessor", CandidateCount: 1, DeltaMS: &delta, CandidateSequence: &sequence, FrameSequenceDelta: &sequenceDelta, CandidateRawSHA256: "aabb", RSSI: &rssi, SNR: &snr},
	}, ReceiverBinding{InstallationID: "00112233445566778899aabbccddeeff", AdapterVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	radio, ok := normalized["radio"].(map[string]any)
	if !ok || len(radio["metrics"].([]any)) != 2 {
		t.Fatalf("accepted heuristic RF was not normalized: %#v", normalized)
	}
	capabilities := normalized["capabilities"].(map[string]any)
	if capabilities["receiver_rssi"] != "available" || capabilities["receiver_snr"] != "available" {
		t.Fatalf("RF capability provenance = %#v", capabilities)
	}
	evidence := normalized["source"].(map[string]any)["evidence"].(map[string]any)["rfEvidence"].(map[string]any)
	if evidence["method"] != "ordered_temporal" || evidence["deterministic"] != false || evidence["outcome"] != "matched_ordered_temporal" || evidence["candidateCount"] != 1 || evidence["associationConfidence"] != "high" || evidence["associationReason"] != "immediate_predecessor" {
		t.Fatalf("RF provenance = %#v", evidence)
	}
	if _, err := receiverevents.Prepare(normalized, startOfRFFixture()); err != nil {
		t.Fatalf("heuristic RF envelope was rejected by the vendored contract: %v", err)
	}
}

func TestNormalizeTaggedTelemetryRFEvidenceDoesNotStageMediumOrLowAssociation(t *testing.T) {
	key := trackingKey
	tag := uint32(0x66554433)
	voltage := 4.09
	sequence := uint64(42)
	delta := int64(1)
	frameDelta := uint64(1)
	immediate := true
	rssi := -77
	snr := 2.5
	for _, association := range []struct {
		confidence string
		reason     string
	}{
		{confidence: "medium", reason: "one_intervening_frame"},
		{confidence: "low", reason: "nearest_preceding_candidate"},
	} {
		t.Run(association.confidence, func(t *testing.T) {
			normalized, err := NormalizeTelemetryResult(TelemetryResult{
				TargetPublicKey: key, SourcePrefix: key[:12], RequestTag: &tag, Tagged: true, ResponseOpcode: PushBinaryResponse,
				ReceivedAt: startOfRFFixture(), RawFrame: []byte{PushBinaryResponse, 0, 0x33, 0x44, 0x55, 0x66, 1, 116, 1, 0x99}, Telemetry: Telemetry{Voltage: &voltage},
				RFEvidence: RFEvidence{
					Method: "ordered_temporal", Deterministic: false, Outcome: "matched_ordered_temporal", AssociationConfidence: association.confidence, AssociationReason: association.reason, CandidateCount: 2,
					RSSI: &rssi, SNR: &snr, NearestCandidateSequence: &sequence, NearestCandidateDeltaMS: &delta, NearestCandidateFrameSequenceDelta: &frameDelta,
					NearestCandidateIsImmediatePredecessor: &immediate, NearestCandidateRSSI: &rssi, NearestCandidateSNR: &snr,
				},
			}, ReceiverBinding{InstallationID: "00112233445566778899aabbccddeeff", AdapterVersion: "test"})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := normalized["radio"]; ok {
				t.Fatalf("%s association created cloud radio metrics: %#v", association.confidence, normalized)
			}
			evidence := normalized["source"].(map[string]any)["evidence"].(map[string]any)
			if _, ok := evidence["rfEvidence"]; ok {
				t.Fatalf("%s association leaked into normalized evidence without end-to-end confidence support: %#v", association.confidence, evidence)
			}
			if _, err := receiverevents.Prepare(normalized, startOfRFFixture()); err != nil {
				t.Fatalf("local-only association changed normalized contract acceptance: %v", err)
			}
		})
	}
}

func startOfRFFixture() time.Time {
	return time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
}
