package diagnostics

import "strings"

type AttentionState string

const (
	AttentionNone           AttentionState = "none"
	AttentionInfo           AttentionState = "info"
	AttentionActionRequired AttentionState = "action_required"
	AttentionUrgent         AttentionState = "urgent"
)

type AttentionCategory string

const (
	AttentionCategoryNone          AttentionCategory = ""
	AttentionCategoryPairing       AttentionCategory = "pairing"
	AttentionCategoryConnectivity  AttentionCategory = "connectivity"
	AttentionCategoryAuthorization AttentionCategory = "authorization"
	AttentionCategoryLifecycle     AttentionCategory = "lifecycle"
	AttentionCategoryNode          AttentionCategory = "node"
	AttentionCategoryForwarding    AttentionCategory = "forwarding"
	AttentionCategoryVersion       AttentionCategory = "version"
	AttentionCategoryCompatibility AttentionCategory = "compatibility"
	AttentionCategoryService       AttentionCategory = "service"
)

type Attention struct {
	State          AttentionState    `json:"state"`
	Category       AttentionCategory `json:"category,omitempty"`
	Code           string            `json:"code,omitempty"`
	Summary        string            `json:"summary,omitempty"`
	Hint           string            `json:"hint,omitempty"`
	ActionRequired bool              `json:"action_required"`
}

func DeriveAttention(finding Finding, ops OperationalSummary) Attention {
	if finding.Code == FailureNone {
		switch strings.ToLower(strings.TrimSpace(ops.Overall)) {
		case "blocked":
			return Attention{
				State:          AttentionActionRequired,
				Category:       AttentionCategoryService,
				Code:           "operational_blocked",
				Summary:        "Receiver requires operator attention",
				Hint:           strings.TrimSpace(ops.Summary),
				ActionRequired: true,
			}
		case "degraded":
			return Attention{
				State:          AttentionInfo,
				Category:       AttentionCategoryService,
				Code:           "operational_degraded",
				Summary:        "Receiver is degraded",
				Hint:           strings.TrimSpace(ops.Summary),
				ActionRequired: false,
			}
		default:
			return Attention{State: AttentionNone}
		}
	}

	category, state := attentionClassForFailure(finding.Code)
	return Attention{
		State:          state,
		Category:       category,
		Code:           string(finding.Code),
		Summary:        strings.TrimSpace(finding.Summary),
		Hint:           strings.TrimSpace(finding.Hint),
		ActionRequired: state == AttentionActionRequired || state == AttentionUrgent,
	}
}

func attentionClassForFailure(code FailureCode) (AttentionCategory, AttentionState) {
	switch code {
	case FailurePairingCodeInvalid, FailurePairingCodeExpired, FailureActivationFailed, FailurePairingNotCompleted:
		return AttentionCategoryPairing, AttentionActionRequired
	case FailureReceiverAuthInvalid:
		return AttentionCategoryAuthorization, AttentionUrgent
	case FailureReceiverRevoked, FailureReceiverDisabled, FailureReceiverReplaced:
		return AttentionCategoryLifecycle, AttentionUrgent
	case FailureCloudConfigIncompat, FailureLocalSchemaIncompat:
		return AttentionCategoryCompatibility, AttentionUrgent
	case FailureReceiverUnsupported:
		return AttentionCategoryVersion, AttentionUrgent
	case FailureReceiverOutdated:
		return AttentionCategoryVersion, AttentionActionRequired
	case FailureCloudUnreachable, FailureNetworkUnavailable, FailurePortalUnavailable:
		return AttentionCategoryConnectivity, AttentionActionRequired
	case FailureNoSerialDevice, FailureNodeNotConnected:
		return AttentionCategoryNode, AttentionActionRequired
	case FailureEventsNotForwarding:
		return AttentionCategoryForwarding, AttentionActionRequired
	default:
		return AttentionCategoryService, AttentionInfo
	}
}
