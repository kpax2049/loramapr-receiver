//go:build linux || darwin

package meshcore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

func openSerial(path string) (io.ReadWriteCloser, error) {
	return openSerialWith(path, func(path string) (*os.File, error) {
		return os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	}, configureSerialFile)
}

func openSerialWith(path string, opener func(string) (*os.File, error), configure func(*os.File) error) (io.ReadWriteCloser, error) {
	file, err := opener(path)
	if err != nil {
		return nil, err
	}
	if err := configure(file); err != nil {
		return nil, fmt.Errorf("configure MeshCore serial device %q: %w", path, errors.Join(err, file.Close()))
	}
	return file, nil
}
