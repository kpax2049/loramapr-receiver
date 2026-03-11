package diagnostics

import (
	"testing"
	"time"
)

func TestEvaluateOperational(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)
	ack := now.Add(-2 * time.Minute)
	queued := now.Add(-3 * time.Minute)

	cases := []struct {
		name       string
		input      OperationalInput
		wantOverall string
	}{
		{
			name: "healthy steady state",
			input: OperationalInput{
				Now:                now,
				Lifecycle:          "running",
				Ready:              true,
				PairingPhase:       "steady_state",
				HasIngestCredential: true,
				CloudReachable:     true,
				MeshtasticState:    "connected",
				LastPacketAck:      &ack,
				LastPacketQueued:   &queued,
				UpdateStatus:       "current",
			},
			wantOverall: "ok",
		},
		{
			name: "unsupported version blocks",
			input: OperationalInput{
				Now:                now,
				Lifecycle:          "running",
				Ready:              true,
				PairingPhase:       "steady_state",
				HasIngestCredential: true,
				CloudReachable:     true,
				MeshtasticState:    "connected",
				UpdateStatus:       "unsupported",
			},
			wantOverall: "blocked",
		},
		{
			name: "unpaired blocks",
			input: OperationalInput{
				Now:             now,
				Lifecycle:       "running",
				Ready:           true,
				PairingPhase:    "unpaired",
				CloudReachable:  false,
				MeshtasticState: "not_present",
				UpdateStatus:    "unknown",
			},
			wantOverall: "blocked",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := EvaluateOperational(tc.input)
			if got.Overall != tc.wantOverall {
				t.Fatalf("expected overall %q, got %q", tc.wantOverall, got.Overall)
			}
			if len(got.Checks) < 6 {
				t.Fatalf("expected full checks list, got %d", len(got.Checks))
			}
		})
	}
}
