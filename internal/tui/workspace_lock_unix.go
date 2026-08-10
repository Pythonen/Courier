//go:build !windows

package tui

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockWorkspaceFile(file *os.File) error {
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}

func unlockWorkspaceFile(file *os.File) error {
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_UN)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}
