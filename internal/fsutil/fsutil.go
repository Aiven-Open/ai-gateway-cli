// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package fsutil holds file helpers shared by commands.
package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteAtomic writes data to path with mode through an exclusive temp file in the same directory and a rename,
// creating missing parent directories 0700.
func WriteAtomic(path string, data []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}()
	// Chmod before writing so the contents never sit in a file looser than mode.
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
