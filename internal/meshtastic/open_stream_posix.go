//go:build linux || darwin

package meshtastic

import (
	"io"
	"os"
	"syscall"
)

func openReadWriteCloser(path string) (io.ReadWriteCloser, error) {
	flags := os.O_RDWR | syscall.O_NOCTTY
	file, err := os.OpenFile(path, flags, 0)
	if err == nil {
		return file, nil
	}
	readOnly, readOnlyErr := os.Open(path)
	if readOnlyErr != nil {
		return nil, err
	}
	return &readOnlyStream{ReadCloser: readOnly}, nil
}
