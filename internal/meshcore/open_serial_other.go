//go:build !linux && !darwin

package meshcore

import (
	"io"
	"os"
)

func openSerial(path string) (io.ReadWriteCloser, error) {
	return os.OpenFile(path, os.O_RDWR, 0)
}
