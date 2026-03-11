package diagnostics

import (
	"strings"
	"testing"
)

func TestProbeLocalRuntimeStatusInvalid(t *testing.T) {
	t.Parallel()

	probe := ProbeLocalRuntimeStatus("invalid", 0)
	if probe.Status != "invalid" {
		t.Fatalf("expected invalid probe, got %q", probe.Status)
	}
}

func TestProbeLocalRuntimeStatusUnreachable(t *testing.T) {
	t.Parallel()

	probe := ProbeLocalRuntimeStatus("0.0.0.0:1", 0)
	if probe.Status != "unreachable" {
		t.Fatalf("expected unreachable probe, got %q", probe.Status)
	}
	if !strings.Contains(probe.URL, "127.0.0.1:1") {
		t.Fatalf("expected probe URL to normalize host to loopback, got %q", probe.URL)
	}
}
