//go:build linux

package meshtastic

import (
	"log/slog"
	"os"
	"syscall"
	"unsafe"
)

func configureSerialFile(file *os.File) {
	if file == nil {
		return
	}
	info, err := file.Stat()
	if err != nil {
		return
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return
	}
	if err := configureSerialFD(int(file.Fd())); err != nil {
		slog.Debug("meshtastic serial fd configuration skipped", "fd", file.Fd(), "err", err)
	}
}

func configureSerialFD(fd int) error {
	var termios syscall.Termios
	if _, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.TCGETS),
		uintptr(unsafe.Pointer(&termios)),
		0,
		0,
		0,
	); errno != 0 {
		return errno
	}

	termios.Iflag &^= syscall.IGNBRK |
		syscall.BRKINT |
		syscall.PARMRK |
		syscall.ISTRIP |
		syscall.INLCR |
		syscall.IGNCR |
		syscall.ICRNL |
		syscall.IXON
	termios.Oflag &^= syscall.OPOST
	termios.Lflag &^= syscall.ECHO |
		syscall.ECHONL |
		syscall.ICANON |
		syscall.ISIG |
		syscall.IEXTEN
	termios.Cflag &^= syscall.CSIZE | syscall.PARENB
	termios.Cflag |= syscall.CS8 | syscall.CLOCAL | syscall.CREAD
	termios.Cc[syscall.VMIN] = 1
	termios.Cc[syscall.VTIME] = 1

	if _, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.TCSETS),
		uintptr(unsafe.Pointer(&termios)),
		0,
		0,
		0,
	); errno != 0 {
		return errno
	}

	lines := syscall.TIOCM_DTR | syscall.TIOCM_RTS
	if _, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.TIOCMBIS),
		uintptr(unsafe.Pointer(&lines)),
	); errno != 0 {
		return errno
	}
	return nil
}
