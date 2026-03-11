package diagnostics

import (
	"testing"
	"time"
)

func TestEvaluateFailureTaxonomy(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 11, 10, 0, 0, 0, time.UTC)
	queued := now.Add(-2 * time.Minute)
	ack := now.Add(-4 * time.Minute)

	cases := []struct {
		name string
		in   Input
		want FailureCode
	}{
		{
			name: "pairing invalid",
			in: Input{
				PairingLastChange: "pairing_code_invalid",
				Now:               now,
			},
			want: FailurePairingCodeInvalid,
		},
		{
			name: "activation failed",
			in: Input{
				PairingLastChange: "activation_failed",
				Now:               now,
			},
			want: FailureActivationFailed,
		},
		{
			name: "credential revoked",
			in: Input{
				PairingLastChange: "credential_revoked",
				Now:               now,
			},
			want: FailureReceiverRevoked,
		},
		{
			name: "receiver disabled",
			in: Input{
				PairingLastChange: "receiver_disabled",
				Now:               now,
			},
			want: FailureReceiverDisabled,
		},
		{
			name: "receiver replaced",
			in: Input{
				PairingLastChange: "receiver_replaced",
				Now:               now,
			},
			want: FailureReceiverReplaced,
		},
		{
			name: "cloud unreachable",
			in: Input{
				PairingPhase:    "steady_state",
				CloudReachable:  false,
				MeshtasticState: "connected",
				Now:             now,
			},
			want: FailureCloudUnreachable,
		},
		{
			name: "cloud config incompatible",
			in: Input{
				PairingPhase:     "steady_state",
				CloudReachable:   false,
				RuntimeLastError: "cloud config version unsupported",
				Now:              now,
			},
			want: FailureCloudConfigIncompat,
		},
		{
			name: "auth invalid",
			in: Input{
				PairingPhase:     "steady_state",
				CloudReachable:   true,
				RuntimeLastError: "cloud api error status=401",
				Now:              now,
			},
			want: FailureReceiverAuthInvalid,
		},
		{
			name: "no serial device",
			in: Input{
				MeshtasticState: "not_present",
				Now:             now,
			},
			want: FailureNoSerialDevice,
		},
		{
			name: "node not connected",
			in: Input{
				MeshtasticState: "detected",
				Now:             now,
			},
			want: FailureNodeNotConnected,
		},
		{
			name: "events not forwarding",
			in: Input{
				MeshtasticState:  "connected",
				IngestQueueDepth: 5,
				LastPacketQueued: &queued,
				LastPacketAck:    &ack,
				Now:              now,
			},
			want: FailureEventsNotForwarding,
		},
		{
			name: "network unavailable",
			in: Input{
				RuntimeProfile:        "appliance-pi",
				NetworkAvailableKnown: true,
				NetworkAvailable:      false,
				MeshtasticState:       "connected",
				Now:                   now,
			},
			want: FailureNetworkUnavailable,
		},
		{
			name: "portal unavailable",
			in: Input{
				RuntimeProfile: "appliance-pi",
				PortalState:    "error",
				Now:            now,
			},
			want: FailurePortalUnavailable,
		},
		{
			name: "pairing not completed appliance",
			in: Input{
				RuntimeProfile:  "appliance-pi",
				PairingPhase:    "unpaired",
				MeshtasticState: "connected",
				Now:             now,
			},
			want: FailurePairingNotCompleted,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			finding := Evaluate(tc.in)
			if finding.Code != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, finding.Code)
			}
		})
	}
}
