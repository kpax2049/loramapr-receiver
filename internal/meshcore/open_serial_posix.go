//go:build linux || darwin

package meshcore

import (
	"io"
	"os"
	"syscall"
)

func openSerial(path string) (io.ReadWriteCloser, error) {
	file, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	configureSerialFile(file)
	return file, nil
}
