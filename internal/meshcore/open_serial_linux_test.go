//go:build linux

package meshcore

import (
	"os"
	"syscall"
	"testing"
)

func TestMakeSerial115200RawLinux(t *testing.T) {
	termios := syscall.Termios{Iflag: ^uint32(0), Oflag: ^uint32(0), Cflag: ^uint32(0), Lflag: ^uint32(0)}
	makeSerial115200Raw(&termios)
	if termios.Ispeed != syscall.B115200 || termios.Ospeed != syscall.B115200 {
		t.Fatalf("expected 115200 baud, got input=%d output=%d", termios.Ispeed, termios.Ospeed)
	}
	if termios.Cflag&syscall.CS8 == 0 || termios.Cflag&(syscall.PARENB|syscall.CSTOPB|linuxHardwareFlowControl) != 0 {
		t.Fatalf("expected 8N1 with hardware flow control disabled: %#x", termios.Cflag)
	}
	if termios.Iflag&(syscall.IXON|syscall.IXOFF|syscall.IXANY) != 0 {
		t.Fatalf("expected software flow control disabled: %#x", termios.Iflag)
	}
}

func TestConfigureSerialFDOnLinuxPTY(t *testing.T) {
	file, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("PTY unavailable: %v", err)
	}
	defer file.Close()
	if err := configureSerialFD(int(file.Fd())); err != nil {
		t.Fatalf("configure PTY: %v", err)
	}
}
