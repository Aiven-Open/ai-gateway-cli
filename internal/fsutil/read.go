// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package fsutil

import (
	"fmt"
	"io"
	"os"
)

// ReadFile returns the content of the regular file at path, as os.ReadFile does, and refuses anything else: a missing
// file is an os.ErrNotExist error, while a symlink, a FIFO, a device or a directory gets an error naming it. A FIFO
// never blocks the read.
func ReadFile(path string) ([]byte, error) {
	data, _, err := ReadRegular(path)
	return data, err
}

// ReadRegular is ReadFile that also returns the file's permission bits.
func ReadRegular(path string) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if err := regular(path, info); err != nil {
		return nil, 0, err
	}
	f, err := os.OpenFile(path, readFlags, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", path, err)
	}
	return readOpened(f, path, info)
}

func regular(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink, not a regular file; replace it with the file it points to, then retry", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file; fix or move it, then retry", path)
	}
	return nil
}

// readOpened reads f, opened without blocking, if it is still the regular file inspected as info: a swap between the
// inspection and the open is refused rather than read.
func readOpened(f *os.File, path string, info os.FileInfo) ([]byte, os.FileMode, error) {
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", path, err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, 0, fmt.Errorf("%s changed while it was read; retry", path)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", path, err)
	}
	return data, opened.Mode().Perm(), nil
}
