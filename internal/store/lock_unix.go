// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

//go:build !windows

package store

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLockFile takes the exclusive lock of f without waiting. It is false, nil
// when another holder has the lock.
func tryLockFile(f *os.File) (locked bool, err error) {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EWOULDBLOCK), errors.Is(err, unix.EAGAIN):
			return false, nil
		default:
			return false, err
		}
	}
}

// unlockFile releases the lock that tryLockFile took on f.
func unlockFile(f *os.File) error {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_UN)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}
