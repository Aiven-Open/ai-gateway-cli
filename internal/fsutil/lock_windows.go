// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package fsutil

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLock locks f's first byte without waiting; false means another handle holds it.
func tryLock(f *os.File) (bool, error) {
	var ol windows.Overlapped
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func unlock(f *os.File) error {
	var ol windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}
