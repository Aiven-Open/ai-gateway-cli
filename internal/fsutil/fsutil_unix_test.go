// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build unix

package fsutil_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

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

// A FIFO, or a symlink to one, is refused without waiting for a writer that never comes.
func TestReadsRefuseAFIFOWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	d, err := fsutil.OpenDir(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	for _, tc := range []struct {
		name, want string
		read       func() error
	}{
		{"ReadFile fifo", "not a regular file", func() error { _, err := fsutil.ReadFile(fifo); return err }},
		{"ReadFile link", "is a symlink", func() error { _, err := fsutil.ReadFile(link); return err }},
		{"Dir.Read fifo", "not a regular file", func() error { _, _, _, err := d.Read("fifo"); return err }},
		{"Dir.Read link", "is a symlink", func() error { _, _, _, err := d.Read("link"); return err }},
		{"Dir.Backup fifo", "not a regular file", func() error { return d.Backup("fifo", "fifo.bak") }},
		{"Dir.Backup link", "is a symlink", func() error { return d.Backup("link", "link.bak") }},
	} {
		done := make(chan error, 1)
		go func() { done <- tc.read() }()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s blocked", tc.name)
		}
	}
}
