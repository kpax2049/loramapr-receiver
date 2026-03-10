package runtime

import (
	"testing"

	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/state"
)

func TestResolveMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		request  config.RunMode
		phase    state.PairingPhase
		expected config.RunMode
	}{
		{name: "explicit setup", request: config.ModeSetup, phase: state.PairingSteadyState, expected: config.ModeSetup},
		{name: "explicit service", request: config.ModeService, phase: state.PairingUnpaired, expected: config.ModeService},
		{name: "auto unpaired", request: config.ModeAuto, phase: state.PairingUnpaired, expected: config.ModeSetup},
		{name: "auto steady", request: config.ModeAuto, phase: state.PairingSteadyState, expected: config.ModeService},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolveMode(tc.request, tc.phase)
			if got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestDetectRuntimeProfile(t *testing.T) {
	t.Parallel()

	if got := detectRuntimeProfile("/var/lib/loramapr/state.json"); got != "linux-service" {
		t.Fatalf("expected linux-service profile, got %q", got)
	}
	if got := detectRuntimeProfile("C:/Users/test/AppData/Roaming/loramapr/state.json"); got != "windows-user" {
		t.Fatalf("expected windows-user profile, got %q", got)
	}
	if got := detectRuntimeProfile("./data/receiver-state.json"); got != "local-dev" {
		t.Fatalf("expected local-dev profile, got %q", got)
	}
}
