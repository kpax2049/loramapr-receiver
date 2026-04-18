package meshtastic

import "testing"

func TestShouldAssertSerialControlLinesDefaultDisabled(t *testing.T) {
	t.Setenv(serialControlLinesEnv, "")
	if shouldAssertSerialControlLines() {
		t.Fatalf("expected default disabled when env is empty")
	}
}

func TestShouldAssertSerialControlLinesTruthy(t *testing.T) {
	cases := []string{"true", "1", "TRUE", "on", "enabled", "yes", "y"}
	for _, value := range cases {
		t.Run(value, func(t *testing.T) {
			t.Setenv(serialControlLinesEnv, value)
			if !shouldAssertSerialControlLines() {
				t.Fatalf("expected control lines enabled for %q", value)
			}
		})
	}
}

func TestShouldAssertSerialControlLinesFalsy(t *testing.T) {
	cases := []string{"false", "0", "FALSE", "off", "disabled", "no", "n", "invalid"}
	for _, value := range cases {
		t.Run(value, func(t *testing.T) {
			t.Setenv(serialControlLinesEnv, value)
			if shouldAssertSerialControlLines() {
				t.Fatalf("expected control lines disabled for %q", value)
			}
		})
	}
}
