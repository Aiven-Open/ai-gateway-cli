// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenChildRefusesADirectorySwappedForASymlinkAfterInspection(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"agent", "other"} {
		if err := os.Mkdir(filepath.Join(home, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	info, err := root.Lstat("agent")
	if err != nil {
		t.Fatal(err)
	}
	// A link that stays inside the root is one os.Root follows, so only the comparison with info refuses it.
	if err := os.Rename(filepath.Join(home, "agent"), filepath.Join(home, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other", filepath.Join(home, "agent")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "agent")
	if child, err := openChild(root, "agent", path, info); err == nil || !strings.Contains(err.Error(), path+" is not a regular directory") {
		if child != nil {
			_ = child.Close()
		}
		t.Fatalf("openChild after the swap: %v", err)
	}
}
