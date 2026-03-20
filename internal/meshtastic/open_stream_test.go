package meshtastic

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenReadWriteCloserRegularFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "meshtastic-device")
	if err := os.WriteFile(path, []byte("seed"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	stream, err := openReadWriteCloser(path)
	if err != nil {
		t.Fatalf("openReadWriteCloser returned error: %v", err)
	}
	defer stream.Close()

	if _, err := stream.Write([]byte("x")); err != nil {
		t.Fatalf("expected writable stream for regular file: %v", err)
	}
}
