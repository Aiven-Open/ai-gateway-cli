// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

// LockFile is held by every launcher run that changes a key file or ModelPrefsFile, so two runs
// never interleave.
const LockFile = ".config/aiven/ai-gateway.lock"

// LockState takes LockFile under userHome, creating its directory 0700, waiting up to wait. The
// func releases it.
func LockState(userHome string, wait time.Duration) (func(), error) {
	if userHome == "" {
		return nil, ErrNoHome
	}
	path := filepath.Join(userHome, filepath.FromSlash(LockFile))
	// Opened as fsutil.WriteAtomic opens the key file's directory, so a path the key write accepts,
	// such as a symlinked .config, never refuses the lock.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	dir, err := fsutil.OpenDir(filepath.Dir(path), ".")
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	defer func() { _ = dir.Close() }()
	return dir.Lock(filepath.Base(LockFile), wait)
}
