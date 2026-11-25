// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package config

import (
	"fmt"
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
	dir, err := fsutil.OpenDir(userHome, filepath.Dir(filepath.FromSlash(LockFile)))
	if err != nil {
		return nil, fmt.Errorf(
			"lock %s: %w", filepath.Join(userHome, filepath.FromSlash(LockFile)), err,
		)
	}
	defer func() { _ = dir.Close() }()
	return dir.Lock(filepath.Base(LockFile), wait)
}
