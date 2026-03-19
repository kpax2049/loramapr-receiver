package diagnostics

import (
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/status"
)

func TestDeriveSetupIssues(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 19, 10, 0, 0, 0, time.UTC)
	queued := now.Add(-2 * time.Minute)
	ack := now.Add(-5 * time.Minute)

	baseOps := EvaluateOperational(OperationalInput{
		Now:                 now,
		Lifecycle:           "running",
		Ready:               true,
		PairingPhase:        "steady_state",
		HasIngestCredential: true,
		CloudReachable:      true,
		MeshtasticState:     "connected",
		LastPacketQueued:    &queued,
		LastPacketAck:       &ack,
	})

	cases := []struct {
		name      string
		snapshot  status.Snapshot
		ops       OperationalSummary
		wantCodes []string
	}{
		{
			name: "portal localhost binding is flagged on linux package profile",
			snapshot: status.Snapshot{
				RuntimeProfile: "linux-service",
				InstallType:    "linux-package",
				CloudEndpoint:  "https://loramapr.com",
				CloudStatus:    "reachable",
				CloudReachable: true,
				PairingPhase:   "unpaired",
				Components: map[string]status.ComponentStatus{
					"portal": {
						State:   "running",
						Message: "local setup portal listening on 127.0.0.1:8080",
					},
				},
			},
			ops:       OperationalSummary{},
			wantCodes: []string{"portal_bind_localhost"},
		},
		{
			name: "cloud placeholder endpoint and unreachable are flagged",
			snapshot: status.Snapshot{
				PairingPhase:   "unpaired",
				CloudEndpoint:  "https://api.loramapr.example",
				CloudStatus:    "unreachable",
				CloudReachable: false,
			},
			ops:       OperationalSummary{},
			wantCodes: []string{"cloud_base_url_placeholder", "cloud_unreachable"},
		},
		{
			name: "usb protocol mismatch degraded detail is flagged",
			snapshot: status.Snapshot{
				PairingPhase:   "steady_state",
				CloudEndpoint:  "https://loramapr.com",
				CloudStatus:    "reachable",
				CloudReachable: true,
				Components: map[string]status.ComponentStatus{
					"meshtastic": {
						State:   "degraded",
						Message: "device=/dev/ttyACM0 error=native serial stream unreadable",
					},
				},
			},
			ops:       baseOps,
			wantCodes: []string{"usb_protocol_unusable"},
		},
		{
			name: "usb absent is flagged",
			snapshot: status.Snapshot{
				PairingPhase:   "steady_state",
				CloudEndpoint:  "https://loramapr.com",
				CloudStatus:    "reachable",
				CloudReachable: true,
				Components: map[string]status.ComponentStatus{
					"meshtastic": {
						State:   "not_present",
						Message: "no meshtastic device detected",
					},
				},
			},
			ops:       baseOps,
			wantCodes: []string{"usb_device_not_detected"},
		},
		{
			name: "usb detected connecting is flagged",
			snapshot: status.Snapshot{
				PairingPhase:   "steady_state",
				CloudEndpoint:  "https://loramapr.com",
				CloudStatus:    "reachable",
				CloudReachable: true,
				Components: map[string]status.ComponentStatus{
					"meshtastic": {
						State:   "connecting",
						Message: "device=/dev/ttyACM0 packets=0",
					},
				},
			},
			ops:       baseOps,
			wantCodes: []string{"usb_detected_node_not_ready"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			issues := DeriveSetupIssues(tc.snapshot, tc.ops)
			for _, code := range tc.wantCodes {
				if !setupIssuesContain(issues, code) {
					t.Fatalf("expected setup issue %q, got %#v", code, issues)
				}
			}
		})
	}
}

func setupIssuesContain(issues []SetupIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
