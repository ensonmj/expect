// +build darwin dragonfly freebsd netbsd openbsd linux

package expect

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func WithoutEcho(action func() error) error {
	var oldState syscall.Termios
	if _, _, err := syscall.Syscall6(syscall.SYS_IOCTL, os.Stdin.Fd(), ioctlReadTermios,
		uintptr(unsafe.Pointer(&oldState)), 0, 0, 0); err != 0 {
		return err
	}

	newState := oldState
	newState.Lflag &^= unix.ECHO
	newState.Lflag |= unix.ICANON | unix.ISIG
	newState.Iflag |= unix.ICRNL
	if _, _, err := syscall.Syscall6(syscall.SYS_IOCTL, os.Stdin.Fd(), ioctlWriteTermios,
		uintptr(unsafe.Pointer(&newState)), 0, 0, 0); err != 0 {
		return err
	}

	defer syscall.Syscall6(syscall.SYS_IOCTL, os.Stdin.Fd(), ioctlWriteTermios,
		uintptr(unsafe.Pointer(&oldState)), 0, 0, 0)

	return action()
}
