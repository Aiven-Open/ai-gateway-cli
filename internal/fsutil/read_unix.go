// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build unix

package fsutil

import (
	"os"
	"syscall"
)

// readFlags open a FIFO swapped in after inspection without waiting for a writer, and refuse a final symlink.
const readFlags = os.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOFOLLOW

func (d *Dir) setMode(_ string, f *os.File, mode os.FileMode) error {
	return f.Chmod(mode)
}

func copyMode(_, dst *os.File, mode os.FileMode) error {
	return dst.Chmod(mode)
}
