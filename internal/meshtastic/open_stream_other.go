//go:build !linux && !darwin

package meshtastic

import (
	"io"
	"os"
)

func openReadWriteCloser(path string) (io.ReadWriteCloser, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err == nil {
		return file, nil
	}
	readOnly, readOnlyErr := os.Open(path)
	if readOnlyErr != nil {
		return nil, err
	}
	return &readOnlyStream{ReadCloser: readOnly}, nil
}
