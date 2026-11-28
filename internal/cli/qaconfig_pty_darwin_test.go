package cli

import (
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func openTestPty(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	var name [128]byte
	for _, request := range []struct {
		code uintptr
		arg  uintptr
	}{{syscall.TIOCPTYGRANT, 0}, {syscall.TIOCPTYUNLK, 0}, {syscall.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))}} {
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), request.code, request.arg); errno != 0 {
			t.Fatalf("pty ioctl: %v", errno)
		}
	}
	end := 0
	for end < len(name) && name[end] != 0 {
		end++
	}
	slave, err := os.OpenFile(string(name[:end]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	return master, slave
}
