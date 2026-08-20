//go:build linux || darwin

package meshcore

import (
	"errors"
	"os"
	"testing"
)

func TestOpenSerialPropagatesConfigurationFailureAndClosesFile(t *testing.T) {
	path := t.TempDir() + "/device"
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	configured := errors.New("configuration failed")
	var opened *os.File
	_, err = openSerialWith(path, func(path string) (*os.File, error) {
		var openErr error
		opened, openErr = os.OpenFile(path, os.O_RDWR, 0)
		return opened, openErr
	}, func(*os.File) error { return configured })
	if !errors.Is(err, configured) {
		t.Fatalf("expected configuration failure, got %v", err)
	}
	if _, err := opened.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("expected file closed after configuration failure, got %v", err)
	}
}
