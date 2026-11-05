// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build !windows

package creds_test

import (
	"os"
	"path/filepath"
	"testing"
)

func checkPrivateKeyFile(t *testing.T, path string) {
	t.Helper()
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v; want 600", info, err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("directory mode = %v, %v; want 700", info, err)
	}
}
