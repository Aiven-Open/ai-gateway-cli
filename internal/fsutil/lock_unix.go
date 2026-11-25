// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build unix

package fsutil

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLock takes f's flock without waiting; false means another open file description holds it.
func tryLock(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func unlock(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }
