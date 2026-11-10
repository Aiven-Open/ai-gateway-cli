// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package fsutil_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
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

func TestBackupCopiesContentAndModeOrSkipsMissing(t *testing.T) {
	for _, mode := range []os.FileMode{0, 0o600, 0o644} {
		path := filepath.Join(t.TempDir(), "rc")
		if mode != 0 {
			_ = os.WriteFile(path, []byte("old"), mode)
			_ = os.Chmod(path, mode)
		}
		dest := fsutil.BackupPath(path, time.Date(2026, 10, 28, 13, 4, 5, 0, time.FixedZone("CET", 3600)))
		if dest != path+".aiven-20261028T120405Z.bak" {
			t.Fatalf("BackupPath = %s", dest)
		}
		d := openDir(t, filepath.Dir(path))
		if err := d.Backup(filepath.Base(path), filepath.Base(dest)); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(dest)
		if mode == 0 && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing source: stat backup = %v", err)
		} else if mode != 0 {
			wantMode := mode
			if runtime.GOOS == "windows" {
				wantMode = 0o666
			}
			if got, _ := os.ReadFile(dest); err != nil || string(got) != "old" || fi.Mode().Perm() != wantMode {
				t.Fatalf("backup = %q (%v), want old %v", got, err, wantMode)
			}
			_ = os.WriteFile(path, []byte("new"), mode)
			if err := d.Backup(filepath.Base(path), filepath.Base(dest)); err == nil || !strings.Contains(err.Error(), dest+" already exists") {
				t.Fatalf("second backup to %s: %v, want refusal", dest, err)
			}
			if got, _ := os.ReadFile(dest); string(got) != "old" {
				t.Fatalf("backup replaced: %q", got)
			}
			now := time.Date(2026, 10, 28, 12, 4, 5, 0, time.UTC)
			if second := fsutil.BackupPath(path, now); second != path+".aiven-20261028T120405Z-2.bak" {
				t.Fatalf("BackupPath beside a taken name = %s", second)
			}
		}
	}
}

func TestBackupToOneNameSucceedsOnceAmongRacingRuns(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "rc.aiven-20261028T120000Z.bak")
	d := openDir(t, dir)
	errs := make([]error, 8)
	var wg sync.WaitGroup
	for i := range errs {
		src := filepath.Join(dir, "rc"+strconv.Itoa(i))
		if err := os.WriteFile(src, []byte(strconv.Itoa(i)), 0o600); err != nil {
			t.Fatal(err)
		}
		wg.Go(func() { errs[i] = d.Backup(filepath.Base(src), filepath.Base(dest)) })
	}
	wg.Wait()
	won := -1
	for i, err := range errs {
		if err == nil && won < 0 {
			won = i
		} else if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("run %d: %v, want exactly one success and the rest refused", i, err)
		}
	}
	if got, _ := os.ReadFile(dest); won < 0 || string(got) != strconv.Itoa(won) {
		t.Fatalf("backup = %q, want run %d's copy", got, won)
	}
}

func openDir(t *testing.T, path string) *fsutil.Dir {
	t.Helper()
	d, err := fsutil.OpenDir(path, ".")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestOpenDirCreatesMissingDirectoriesAndRefusesASymlink(t *testing.T) {
	home := t.TempDir()
	d, err := fsutil.OpenDir(home, filepath.Join(".pi", "agent"))
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	if info, err := os.Stat(filepath.Join(home, ".pi", "agent")); err != nil || !info.IsDir() || d.Path != filepath.Join(home, ".pi", "agent") {
		t.Fatalf("stat %v, %v; path %s", info, err, d.Path)
	}
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(home, ".codex")); err != nil {
		t.Fatal(err)
	}
	if _, err := fsutil.OpenDir(home, ".codex"); err == nil || !strings.Contains(err.Error(), filepath.Join(home, ".codex")+" is not a regular directory") {
		t.Fatalf("OpenDir through a symlink: %v", err)
	}
}

func TestDirWritesWhereItWasOpenedAfterItsPathIsReplaced(t *testing.T) {
	home := t.TempDir()
	d, err := fsutil.OpenDir(home, "agent")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	moved, elsewhere := filepath.Join(home, "moved"), t.TempDir()
	if err := os.Rename(filepath.Join(home, "agent"), moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(home, "agent")); err != nil {
		t.Fatal(err)
	}
	if err := d.WriteAtomic("models.json", []byte("new"), 0o600, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.Backup("models.json", "models.json.bak"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("wrote through the symlink: %v", entries)
	}
	for _, name := range []string{"models.json", "models.json.bak"} {
		if got, _ := os.ReadFile(filepath.Join(moved, name)); string(got) != "new" {
			t.Errorf("%s = %q, want new in the directory opened", name, got)
		}
	}
	if data, _, existed, err := d.Read("models.json"); err != nil || !existed || string(data) != "new" {
		t.Errorf("Read = %q, %v, %v", data, existed, err)
	}
}

func TestDirWriteAtomicChecksJustBeforeTheRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := openDir(t, dir)
	changed := errors.New("changed since inspection")
	err := d.WriteAtomic("f", []byte("new"), 0o600, func() error {
		// The temp file is complete by now; only the rename is left.
		entries, _ := os.ReadDir(dir)
		if len(entries) != 2 {
			t.Errorf("entries before the rename: %v", entries)
		}
		return changed
	})
	if !errors.Is(err, changed) {
		t.Fatalf("WriteAtomic = %v, want the check's error", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Errorf("f = %q, want it untouched", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("leftovers: %v", entries)
	}
}

func TestReadFileReadsOnlyARegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantPerm := os.FileMode(0o600)
	if runtime.GOOS == "windows" {
		wantPerm = 0o666
	}
	if data, perm, err := fsutil.ReadRegular(path); err != nil || string(data) != "data" || perm != wantPerm {
		t.Errorf("ReadRegular = %q, %v, %v; want data, %v", data, perm, err, wantPerm)
	}
	if _, err := fsutil.ReadFile(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: %v, want os.ErrNotExist", err)
	}
	if _, err := fsutil.ReadFile(dir); err == nil || !strings.Contains(err.Error(), dir+" is not a regular file") {
		t.Errorf("directory: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Skip("no symlink privilege:", err)
	}
	if _, err := fsutil.ReadFile(link); err == nil || !strings.Contains(err.Error(), link+" is a symlink, not a regular file") {
		t.Errorf("symlink: %v", err)
	}
}
