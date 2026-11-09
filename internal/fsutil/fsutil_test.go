// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package fsutil_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Aiven-Open/ai-gateway-cli/internal/fsutil"
)

func TestWriteAtomicIgnoresPlantedTempSymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "f")
	if err := os.Symlink(victim, path+".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := fsutil.WriteAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "keep" {
		t.Fatalf("symlink target overwritten: %q", got)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Fatalf("file = %q, want new", got)
	}
}

func TestWriteAtomicSetsRequestedModeDespiteUmask(t *testing.T) {
	defer syscall.Umask(syscall.Umask(0o077))
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fsutil.WriteAtomic(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, want 0644", fi.Mode().Perm())
	}
}

func TestWriteAtomicLeavesNoTempAfterFailedRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fsutil.WriteAtomic(path, []byte("x"), 0o600); err == nil {
		t.Fatal("want error renaming over a directory")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftovers in %s: %v", dir, entries)
	}
}
