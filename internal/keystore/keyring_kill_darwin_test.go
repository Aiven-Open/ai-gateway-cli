// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build darwin

package keystore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeSecurity is a `security` stand-in that changes state (touches $MARKER) only after a delay.
func fakeSecurity(t *testing.T) (path, marker string) {
	t.Helper()
	dir := t.TempDir()
	path, marker = filepath.Join(dir, "security"), filepath.Join(dir, "written")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 1\ntouch \"$MARKER\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MARKER", marker)
	return path, marker
}

// go-keyring leaves `security` running past a timeout, so a timed-out write could land after a newer login.
func TestTimedOutKeychainWriteNeverLands(t *testing.T) {
	path, marker := fakeSecurity(t)
	k := macKeychain{path: path, timeout: 100 * time.Millisecond}
	if err := k.Set(keyringService, "https://a.example", "{}"); !errors.Is(err, errKeychainTimeout) {
		t.Fatalf("Set = %v, want timeout", err)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("timed-out write landed")
	}
}

func TestKeychainWriteWithinDeadlineLands(t *testing.T) {
	path, marker := fakeSecurity(t)
	k := macKeychain{path: path, timeout: 5 * time.Second}
	if err := k.Set(keyringService, "https://a.example", "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("write did not land")
	}
}
