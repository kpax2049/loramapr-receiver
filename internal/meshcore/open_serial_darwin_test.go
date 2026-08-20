//go:build darwin

package meshcore

import (
	"syscall"
	"testing"
)

func TestMakeSerial115200RawDarwin(t *testing.T) {
	termios := syscall.Termios{Iflag: ^uint64(0), Oflag: ^uint64(0), Cflag: ^uint64(0), Lflag: ^uint64(0)}
	makeSerial115200Raw(&termios)
	if termios.Ispeed != syscall.B115200 || termios.Ospeed != syscall.B115200 {
		t.Fatalf("expected 115200 baud, got input=%d output=%d", termios.Ispeed, termios.Ospeed)
	}
	if termios.Cflag&syscall.CS8 == 0 || termios.Cflag&(syscall.PARENB|syscall.CSTOPB|darwinHardwareFlowControl) != 0 {
		t.Fatalf("expected 8N1 with hardware flow control disabled: %#x", termios.Cflag)
	}
	if termios.Iflag&(syscall.IXON|syscall.IXOFF|syscall.IXANY) != 0 {
		t.Fatalf("expected software flow control disabled: %#x", termios.Iflag)
	}
}
