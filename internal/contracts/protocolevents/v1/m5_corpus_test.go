package v1_test

import (
	"path/filepath"
	"regexp"
	"testing"
)

type m5Corpus struct {
	Version           string       `json:"version"`
	FixtureProvenance string       `json:"fixtureProvenance"`
	Scenarios         []m5Scenario `json:"scenarios"`
}

type m5Scenario struct {
	ID       string            `json:"id"`
	Protocol string            `json:"protocol"`
	Exercise string            `json:"exercise"`
	Expected map[string]string `json:"expected"`
}

func TestM5DeterministicRegressionCorpusManifest(t *testing.T) {
	t.Parallel()
	corpus := readJSON[m5Corpus](t, filepath.Join(packageRoot(t), "m5-corpus.json"))
	if corpus.Version != "1.0" {
		t.Fatalf("corpus version = %q, want 1.0", corpus.Version)
	}
	if !regexp.MustCompile(`(?i)source-derived/synthetic`).MatchString(corpus.FixtureProvenance) ||
		!regexp.MustCompile(`(?i)not physical hardware captures`).MatchString(corpus.FixtureProvenance) {
		t.Fatalf("fixture provenance must explicitly distinguish source-derived fixtures from hardware captures: %q", corpus.FixtureProvenance)
	}
	if len(corpus.Scenarios) != 40 {
		t.Fatalf("scenario count = %d, want 40", len(corpus.Scenarios))
	}
	ids := make(map[string]struct{}, len(corpus.Scenarios))
	validID := regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)+$`)
	for _, scenario := range corpus.Scenarios {
		if !validID.MatchString(scenario.ID) || scenario.Protocol == "" || scenario.Exercise == "" || len(scenario.Expected) == 0 {
			t.Fatalf("invalid deterministic scenario: %#v", scenario)
		}
		if _, exists := ids[scenario.ID]; exists {
			t.Fatalf("duplicate scenario ID %q", scenario.ID)
		}
		ids[scenario.ID] = struct{}{}
		for field, outcome := range scenario.Expected {
			if field == "" || outcome == "" {
				t.Fatalf("scenario %q has an incomplete expected outcome: %#v", scenario.ID, scenario.Expected)
			}
		}
	}
	for _, required := range []string{
		"delivery.exact-normalized-retry",
		"ordering.current-position-monotonic",
		"identity.protocol-qualified-collision",
		"auth.raw-signed-0x88",
		"rf.quarter-db-snr-once",
		"onboarding.three-proofs",
		"has.cloud-attestation-only",
		"meshtastic.measurement-session-coverage",
	} {
		if _, exists := ids[required]; !exists {
			t.Fatalf("required M5 scenario %q is absent", required)
		}
	}
}
