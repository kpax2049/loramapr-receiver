package runtime

import "testing"

func TestResolveLocalNameHint(t *testing.T) {
	t.Parallel()

	if got := resolveLocalNameHint("Back Patio Receiver", "persisted-name", "pi-host", "pi-appliance", "abcdef123456"); got != "Back Patio Receiver" {
		t.Fatalf("expected config local name override, got %q", got)
	}
	if got := resolveLocalNameHint("", "persisted-name", "pi-host", "pi-appliance", "abcdef123456"); got != "persisted-name" {
		t.Fatalf("expected persisted local name, got %q", got)
	}
	if got := resolveLocalNameHint("", "", "pi-host", "pi-appliance", "abcdef123456"); got != "pi-host-123456" {
		t.Fatalf("unexpected derived local name: %q", got)
	}
}

func TestDefaultLocalNameHintByInstallType(t *testing.T) {
	t.Parallel()

	if got := defaultLocalNameHint("", "pi-appliance", "abcdef123456"); got != "pi-receiver-123456" {
		t.Fatalf("unexpected appliance default: %q", got)
	}
	if got := defaultLocalNameHint("", "linux-package", "abcdef123456"); got != "linux-receiver-123456" {
		t.Fatalf("unexpected linux default: %q", got)
	}
	if got := defaultLocalNameHint("", "manual", "abcdef123456"); got != "receiver-123456" {
		t.Fatalf("unexpected manual default: %q", got)
	}
}

func TestSanitizeLocalNameHint(t *testing.T) {
	t.Parallel()

	if got := sanitizeLocalNameHint("  Garage Receiver #1\n"); got != "Garage Receiver 1" {
		t.Fatalf("unexpected sanitized local name: %q", got)
	}
	veryLong := "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz"
	if got := sanitizeLocalNameHint(veryLong); len(got) > maxIdentityHintLength {
		t.Fatalf("expected sanitized local name to be trimmed, got %d chars", len(got))
	}
}
