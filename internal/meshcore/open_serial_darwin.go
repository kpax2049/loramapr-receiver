//go:build darwin

package meshcore

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const darwinHardwareFlowControl = 0x00010000 | 0x00020000

func configureSerialFile(file *os.File) error {
	if file == nil {
		return fmt.Errorf("serial file is nil")
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("serial device is not a character device")
	}
	return configureSerialFD(int(file.Fd()))
}

func configureSerialFD(fd int) error {
	var termios syscall.Termios
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TIOCGETA), uintptr(unsafe.Pointer(&termios)), 0, 0, 0); errno != 0 {
		return errno
	}
	makeSerial115200Raw(&termios)
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TIOCSETA), uintptr(unsafe.Pointer(&termios)), 0, 0, 0); errno != 0 {
		return errno
	}
	return nil
}

func makeSerial115200Raw(termios *syscall.Termios) {
	termios.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON | syscall.IXOFF | syscall.IXANY
	termios.Oflag &^= syscall.OPOST
	termios.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	termios.Cflag &^= syscall.CSIZE | syscall.PARENB | syscall.PARODD | syscall.CSTOPB | darwinHardwareFlowControl
	termios.Cflag |= syscall.CS8 | syscall.CLOCAL | syscall.CREAD
	termios.Ispeed = syscall.B115200
	termios.Ospeed = syscall.B115200
	termios.Cc[syscall.VMIN] = 1
	termios.Cc[syscall.VTIME] = 0
}
