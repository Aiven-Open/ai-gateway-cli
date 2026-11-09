// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package keystore_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Aiven-Open/ai-gateway-cli/internal/keystore"
)

func TestFileStoreTreatsEquivalentGatewayURLsAsOne(t *testing.T) {
	f := keystore.NewFile(t.TempDir())
	if _, err := f.Set(keystore.Record{BaseURL: "HTTPS://AI.Aiven.io/", Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"https://ai.aiven.io", "https://ai.aiven.io/", "https://AI.aiven.io/v1"} {
		rec, err := f.Get(u)
		if err != nil || rec.Key != "k1" || rec.BaseURL != "https://ai.aiven.io" {
			t.Errorf("Get(%q) = %+v, %v", u, rec, err)
		}
	}
	if _, err := f.Get("https://ai.aiven.io:8443"); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("other port: err = %v, want ErrNotFound", err)
	}
}

func TestFileStoreKeepsGatewaysSeparate(t *testing.T) {
	f := keystore.NewFile(t.TempDir())
	_, _ = f.Set(keystore.Record{BaseURL: "https://a.example", Key: "ka"})
	_, _ = f.Set(keystore.Record{BaseURL: "https://b.example", Key: "kb"})
	if err := f.Delete("https://a.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get("https://a.example"); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("a: err = %v", err)
	}
	if rec, err := f.Get("https://b.example"); err != nil || rec.Key != "kb" {
		t.Errorf("b = %+v, %v", rec, err)
	}
}

func TestFileStoreIgnoresRecordForAnotherGateway(t *testing.T) {
	home := t.TempDir()
	f := keystore.NewFile(home)
	path, _ := f.Set(keystore.Record{BaseURL: "https://a.example", Key: "ka"})
	// A record copied under another gateway's file name must not be served for it.
	data, _ := os.ReadFile(path)
	other, _ := keystore.NewFile(t.TempDir()).Set(keystore.Record{BaseURL: "https://b.example", Key: "x"})
	if err := os.WriteFile(filepath.Join(home, "aig-keys", filepath.Base(other)), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get("https://b.example"); !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestFileStoreTreatsCorruptRecordAsMissing(t *testing.T) {
	f := keystore.NewFile(t.TempDir())
	path, _ := f.Set(keystore.Record{BaseURL: "https://a.example", Key: "ka"})
	_ = os.WriteFile(path, []byte("{"), 0o600)
	if _, err := f.Get("https://a.example"); !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestFileStoreSetTightensLooseKeyDirectory(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "aig-keys")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := keystore.NewFile(home).Set(keystore.Record{BaseURL: "https://a.example", Key: "ka"}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", fi.Mode().Perm())
	}
}

func TestFileStoreSetWritesMode0600OverLooserFile(t *testing.T) {
	f := keystore.NewFile(t.TempDir())
	path, _ := f.Set(keystore.Record{BaseURL: "https://a.example", Key: "ka"})
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Set(keystore.Record{BaseURL: "https://a.example", Key: "kb"}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestFileStoreSetIgnoresPlantedTempSymlink(t *testing.T) {
	home := t.TempDir()
	f := keystore.NewFile(home)
	path, _ := f.Set(keystore.Record{BaseURL: "https://a.example", Key: "ka"})
	victim := filepath.Join(home, "victim")
	_ = os.WriteFile(victim, []byte("keep"), 0o600)
	if err := os.Symlink(victim, path+".tmp"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Set(keystore.Record{BaseURL: "https://a.example", Key: "kb"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "keep" {
		t.Fatalf("symlink target overwritten: %q", got)
	}
}
