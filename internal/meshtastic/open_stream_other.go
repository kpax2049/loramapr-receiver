//go:build !linux && !darwin

package meshtastic

import (
	"io"
	"os"
)

func openReadOnlyCloser(path string) (io.ReadWriteCloser, error) {
	readOnly, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &readOnlyStream{ReadCloser: readOnly}, nil
}

func openReadWriteCloser(path string) (io.ReadWriteCloser, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err == nil {
		return file, nil
	}
	readOnly, readOnlyErr := openReadOnlyCloser(path)
	if readOnlyErr != nil {
		return nil, err
	}
	return readOnly, nil
}
