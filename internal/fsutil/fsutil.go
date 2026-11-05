// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package fsutil holds file helpers shared by commands.
package fsutil

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WriteAtomic writes data to path with mode through an exclusive temp file in the same directory and a rename,
// creating missing parent directories 0700.
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	d, err := OpenDir(dir, ".")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer func() { _ = d.Close() }()
	return d.WriteAtomic(filepath.Base(path), data, mode, nil)
}

// Dir is a directory opened once, so what is written through it lands there even if its path later names another
// directory, such as one a symlink points to.
type Dir struct {
	root *os.Root
	// Path is the directory's path when it was opened.
	Path string
}

// OpenDir opens the directory rel below base one component at a time, creating missing ones 0700. A component that
// is a symlink or not a directory is refused, so the directory opened is the one rel names, even if a component is
// swapped while it is opened.
func OpenDir(base, rel string) (*Dir, error) {
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	path := base
	for _, name := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		if name == "." {
			continue
		}
		path = filepath.Join(path, name)
		if err := root.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			_ = root.Close()
			return nil, err
		}
		info, err := root.Lstat(name)
		if err != nil {
			_ = root.Close()
			return nil, err
		}
		next, err := openChild(root, name, path, info)
		_ = root.Close()
		if err != nil {
			return nil, err
		}
		root = next
	}
	return &Dir{root: root, Path: path}, nil
}

func openChild(root *os.Root, name, path string, info os.FileInfo) (*os.Root, error) {
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a regular directory", path)
	}
	child, err := root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	// OpenRoot follows a symlink swapped in after the Lstat; the directory opened must be the one inspected.
	if opened, err := child.Stat("."); err != nil || !os.SameFile(info, opened) {
		_ = child.Close()
		return nil, fmt.Errorf("%s is not a regular directory", path)
	}
	return child, nil
}

// Close releases the directory.
func (d *Dir) Close() error { return d.root.Close() }

func (d *Dir) openRegular(name string) (*os.File, os.FileInfo, error) {
	path := filepath.Join(d.Path, name)
	info, err := d.root.Lstat(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("inspect %s: %w", path, err)
	}
	if err := regular(path, info); err != nil {
		return nil, nil, err
	}
	f, err := d.root.OpenFile(name, readFlags, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	opened, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = f.Close()
		return nil, nil, fmt.Errorf("%s changed while it was read; retry", path)
	}
	return f, opened, nil
}

// Read returns the content and permission bits of the regular file name and whether it exists, refusing anything else
// as ReadFile does.
func (d *Dir) Read(name string) ([]byte, os.FileMode, bool, error) {
	f, info, err := d.openRegular(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	data, perm, err := readOpened(f, filepath.Join(d.Path, name), info)
	return data, perm, err == nil, err
}

// WriteAtomic replaces name with data and mode through an exclusive temp file and a rename. check, when set, runs
// once the temp file is complete and just before the rename; its error leaves name as it was.
func (d *Dir) WriteAtomic(name string, data []byte, mode os.FileMode, check func() error) (err error) {
	path := filepath.Join(d.Path, name)
	tmp := name + ".tmp" + rand.Text()
	f, err := d.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = d.root.Remove(tmp)
		}
	}()
	// Chmod before writing so the contents never sit in a file looser than mode.
	if err = d.setMode(name, f, mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if check != nil {
		if err = check(); err != nil {
			return err
		}
	}
	if err = d.root.Rename(tmp, name); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// BackupPath names the copy Backup makes of path at now, the scheme the Aiven Console's instructions use too, so
// every run that replaces a file keeps its own copy. A name another run took within the same second gets a -2, -3, …
// suffix; past -100 the name stays taken and Backup refuses it.
func BackupPath(path string, now time.Time) string {
	base := path + ".aiven-" + now.UTC().Format("20060102T150405Z")
	name := base + ".bak"
	for i := 2; i <= 100; i++ {
		if _, err := os.Lstat(name); errors.Is(err, os.ErrNotExist) {
			break
		}
		name = fmt.Sprintf("%s-%d.bak", base, i)
	}
	return name
}

// Backup copies name to dest, both in d, with the same permission bits; a missing name needs no backup. dest is
// created exclusively, so of two runs that chose the same name only one succeeds, and a copy once made is never
// replaced.
func (d *Dir) Backup(name, dest string) (err error) {
	path, destPath := filepath.Join(d.Path, name), filepath.Join(d.Path, dest)
	src, info, err := d.openRegular(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("back up %s: %w", path, err)
	}
	defer func() { _ = src.Close() }()
	f, err := d.root.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("back up %s: %s already exists; retry", path, destPath)
	} else if err != nil {
		return fmt.Errorf("back up %s: %w", path, err)
	}
	// A partial copy is no backup; removing it keeps the name free instead of pointing the user at it.
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = d.root.Remove(dest)
			err = fmt.Errorf("back up %s: %w", path, err)
		}
	}()
	if err = copyMode(src, f, info.Mode().Perm()); err == nil {
		_, err = f.ReadFrom(src)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	return err
}
