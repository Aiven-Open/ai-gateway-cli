// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package keystore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Aiven-Open/ai-gateway-cli/internal/fsutil"
)

// File keeps records as 0600 JSON files under <home>/aig-keys.
type File struct{ dir string }

// NewFile returns a file backend rooted at the launcher home.
func NewFile(home string) *File { return &File{dir: filepath.Join(home, "aig-keys")} }

// Get reads the record for baseURL; an unreadable or mismatched file counts as missing.
func (f *File) Get(baseURL string) (Record, error) {
	canon := Canonical(baseURL)
	data, err := os.ReadFile(f.path(canon))
	if err != nil {
		return Record{}, ErrNotFound
	}
	var rec Record
	if json.Unmarshal(data, &rec) != nil || rec.BaseURL != canon || rec.Key == "" {
		return Record{}, ErrNotFound
	}
	return rec, nil
}

// Set writes rec atomically and returns the file path.
func (f *File) Set(rec Record) (string, error) {
	rec.BaseURL = Canonical(rec.BaseURL)
	data, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		return "", fmt.Errorf("create key directory: %w", err)
	}
	if err := os.Chmod(f.dir, 0o700); err != nil {
		return "", fmt.Errorf("secure key directory: %w", err)
	}
	path := f.path(rec.BaseURL)
	if err := fsutil.WriteAtomic(path, data, 0o600); err != nil {
		return "", fmt.Errorf("store key: %w", err)
	}
	return path, nil
}

// Delete removes the record; an absent one is not an error.
func (f *File) Delete(baseURL string) error {
	err := os.Remove(f.path(Canonical(baseURL)))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (f *File) path(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return filepath.Join(f.dir, hex.EncodeToString(sum[:])+".json")
}
