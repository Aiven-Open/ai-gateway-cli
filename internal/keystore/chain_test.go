// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package keystore_test

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/Aiven-Open/ai-gateway-cli/internal/keystore"
)

// fakeKeyring is an in-memory Store whose writes can be made to fail.
type fakeKeyring struct {
	recs       map[string]keystore.Record
	failSet    bool
	failDelete bool
	failGet    bool
	gets       int
}

func newFakeKeyring() *fakeKeyring { return &fakeKeyring{recs: map[string]keystore.Record{}} }

func (f *fakeKeyring) Get(u string) (keystore.Record, error) {
	f.gets++
	if f.failGet {
		return keystore.Record{}, errors.New("keychain did not respond")
	}
	if rec, ok := f.recs[keystore.Canonical(u)]; ok {
		return rec, nil
	}
	return keystore.Record{}, keystore.ErrNotFound
}

func (f *fakeKeyring) Set(rec keystore.Record) (string, error) {
	if f.failSet {
		return "", errors.New("keychain locked")
	}
	rec.BaseURL = keystore.Canonical(rec.BaseURL)
	f.recs[rec.BaseURL] = rec
	return "fake keychain", nil
}

func (f *fakeKeyring) Delete(u string) error {
	if f.failDelete {
		return errors.New("dbus: no session bus")
	}
	delete(f.recs, keystore.Canonical(u))
	return nil
}

const gw = "https://ai.aiven.io"

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newChain(t *testing.T) (*keystore.Chain, *fakeKeyring, *keystore.File, *strings.Builder) {
	t.Helper()
	kr, file, notice := newFakeKeyring(), keystore.NewFile(t.TempDir()), &strings.Builder{}
	return &keystore.Chain{Primary: kr, Fallback: file, Notice: notice}, kr, file, notice
}

func TestChainWriteToKeyringRemovesFileRecord(t *testing.T) {
	c, kr, file, notice := newChain(t)
	_, _ = file.Set(keystore.Record{BaseURL: gw, Key: "old", WrittenAt: t0})
	where, err := c.Set(keystore.Record{BaseURL: gw, Key: "new", WrittenAt: t0.Add(time.Hour)})
	if err != nil || where != "fake keychain" {
		t.Fatalf("Set = %q, %v", where, err)
	}
	if _, err := file.Get(gw); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("file record survived: err = %v", err)
	}
	if rec, _ := kr.Get(gw); rec.Key != "new" {
		t.Errorf("keyring key = %q", rec.Key)
	}
	if notice.Len() != 0 {
		t.Errorf("unexpected notice %q", notice)
	}
}

func TestChainWriteFallsBackToFileWithOneNotice(t *testing.T) {
	c, kr, file, notice := newChain(t)
	kr.failSet = true
	where, err := c.Set(keystore.Record{BaseURL: gw, Key: "k", WrittenAt: t0})
	if err != nil || !strings.HasSuffix(where, ".json") {
		t.Fatalf("Set = %q, %v; want file path", where, err)
	}
	if rec, err := file.Get(gw); err != nil || rec.Key != "k" {
		t.Errorf("file record = %+v, %v", rec, err)
	}
	if n := strings.Count(notice.String(), "\n"); n != 1 || !strings.Contains(notice.String(), "keychain locked") {
		t.Errorf("notice = %q, want one line naming the cause", notice)
	}
}

func TestChainReadPrefersFileRecordWithoutTouchingKeychain(t *testing.T) {
	c, kr, file, _ := newChain(t)
	_, _ = kr.Set(keystore.Record{BaseURL: gw, Key: "keyring", WrittenAt: t0})
	_, _ = file.Set(keystore.Record{BaseURL: gw, Key: "file", WrittenAt: t0.Add(-time.Hour)})
	if rec, err := c.Get(gw); err != nil || rec.Key != "file" {
		t.Fatalf("Get = %+v, %v; want file", rec, err)
	}
	if kr.gets != 0 {
		t.Errorf("keychain read %d times, want 0: a timing-out keychain would stall every launch", kr.gets)
	}
}

func TestChainReadFallsBackToKeychainWhenNoFileRecord(t *testing.T) {
	c, kr, _, _ := newChain(t)
	_, _ = kr.Set(keystore.Record{BaseURL: gw, Key: "keyring", WrittenAt: t0})
	if rec, err := c.Get(gw); err != nil || rec.Key != "keyring" {
		t.Fatalf("Get = %+v, %v; want keyring", rec, err)
	}
}

func TestChainReadIgnoresRecordForAnotherGateway(t *testing.T) {
	c, kr, file, _ := newChain(t)
	kr.recs[gw] = keystore.Record{BaseURL: "https://other.example", Key: "wrong", WrittenAt: t0.Add(time.Hour)}
	_, _ = file.Set(keystore.Record{BaseURL: gw, Key: "right", WrittenAt: t0})
	if rec, err := c.Get(gw); err != nil || rec.Key != "right" {
		t.Fatalf("Get = %+v, %v; want right", rec, err)
	}
	delete(kr.recs, gw)
	_ = file.Delete(gw)
	if _, err := c.Get(gw); !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestChainDeleteClearsBothBackends(t *testing.T) {
	c, kr, file, _ := newChain(t)
	_, _ = kr.Set(keystore.Record{BaseURL: gw, Key: "k", WrittenAt: t0})
	_, _ = file.Set(keystore.Record{BaseURL: gw, Key: "k", WrittenAt: t0})
	if err := c.Delete(gw); err != nil {
		t.Fatal(err)
	}
	if _, err := kr.Get(gw); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("keyring record survived: %v", err)
	}
	if _, err := file.Get(gw); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("file record survived: %v", err)
	}
}

func TestChainDeleteSucceedsWithNoticeWhenKeychainUnavailable(t *testing.T) {
	c, kr, file, notice := newChain(t)
	kr.failDelete = true
	_, _ = file.Set(keystore.Record{BaseURL: gw, Key: "k", WrittenAt: t0})
	if err := c.Delete(gw); err != nil {
		t.Fatalf("Delete = %v, want nil once the file record is gone", err)
	}
	if _, err := file.Get(gw); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("file record survived: %v", err)
	}
	if strings.Count(notice.String(), "\n") != 1 || !strings.Contains(notice.String(), "no session bus") {
		t.Errorf("notice = %q, want one line naming the keychain error", notice)
	}
}

func TestChainDeleteFailsWhenKeychainStillServesKey(t *testing.T) {
	c, kr, file, _ := newChain(t)
	_, _ = kr.Set(keystore.Record{BaseURL: gw, Key: "k", WrittenAt: t0})
	_, _ = file.Set(keystore.Record{BaseURL: gw, Key: "k", WrittenAt: t0})
	kr.failDelete = true
	if err := c.Delete(gw); err == nil || !strings.Contains(err.Error(), "no session bus") {
		t.Fatalf("Delete = %v, want keychain error: Get would keep serving the key", err)
	}
	if _, err := file.Get(gw); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("file record survived: %v", err)
	}
}

func TestChainKeepsGatewaysSeparate(t *testing.T) {
	c, _, _, _ := newChain(t)
	_, _ = c.Set(keystore.Record{BaseURL: "https://a.example", Key: "ka", WrittenAt: t0})
	_, _ = c.Set(keystore.Record{BaseURL: "https://b.example", Key: "kb", WrittenAt: t0})
	if err := c.Delete("https://a.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get("https://a.example"); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("a: err = %v", err)
	}
	if rec, err := c.Get("https://b.example"); err != nil || rec.Key != "kb" {
		t.Errorf("b = %+v, %v", rec, err)
	}
}

func TestChainOverKeychainRoundTripsAndSurvivesStaleFile(t *testing.T) {
	keyring.MockInit()
	home := t.TempDir()
	file := keystore.NewFile(home)
	c := &keystore.Chain{Primary: keystore.NewKeyring(), Fallback: file, Notice: &strings.Builder{}}
	_, _ = file.Set(keystore.Record{BaseURL: gw, Key: "old", WrittenAt: t0})
	where, err := c.Set(keystore.Record{BaseURL: gw, Key: "new", KeyID: "kid", WrittenAt: t0.Add(time.Hour)})
	if err != nil || where != "OS keychain" {
		t.Fatalf("Set = %q, %v", where, err)
	}
	if rec, err := c.Get(gw); err != nil || rec.Key != "new" || rec.KeyID != "kid" {
		t.Fatalf("Get = %+v, %v", rec, err)
	}
	if _, err := file.Get(gw); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("file record survived: %v", err)
	}
}

func TestNewSelectsBackendByMode(t *testing.T) {
	keyring.MockInit()
	notice := &strings.Builder{}
	rec := keystore.Record{BaseURL: gw, Key: "k", WrittenAt: t0}
	for mode, want := range map[string]string{"file": ".json", "keyring": "OS keychain", "auto": "OS keychain"} {
		s, err := keystore.New(mode, t.TempDir(), notice)
		if err != nil {
			t.Fatalf("New(%q): %v", mode, err)
		}
		if where, err := s.Set(rec); err != nil || !strings.HasSuffix(where, want) {
			t.Errorf("mode %q: Set = %q, %v; want suffix %q", mode, where, err, want)
		}
	}
}

func TestNewRejectsUnknownMode(t *testing.T) {
	_, err := keystore.New("vault", t.TempDir(), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), `"vault"`) || !strings.Contains(err.Error(), "file, keyring or auto") {
		t.Fatalf("err = %v, want message naming the value and valid modes", err)
	}
}

// failingDelete is a Store whose Delete fails, as an unremovable key file would.
type failingDelete struct{ *keystore.File }

func (failingDelete) Delete(string) error {
	return &fs.PathError{Op: "remove", Path: "/home/ada/aig-keys/abc.json", Err: errors.New("permission denied")}
}

func TestChainWriteOverwritesStaleFileWhenItCannotBeRemoved(t *testing.T) {
	kr, notice := newFakeKeyring(), &strings.Builder{}
	file := keystore.NewFile(t.TempDir())
	_, _ = file.Set(keystore.Record{BaseURL: gw, Key: "old", WrittenAt: t0})
	c := &keystore.Chain{Primary: kr, Fallback: failingDelete{file}, Notice: notice}
	where, err := c.Set(keystore.Record{BaseURL: gw, Key: "new", WrittenAt: t0.Add(time.Hour)})
	if err != nil || where != "fake keychain" {
		t.Fatalf("Set = %q, %v; want success in keychain", where, err)
	}
	if rec, _ := c.Get(gw); rec.Key != "new" {
		t.Errorf("Get key = %q, want new: reads prefer the file, so it must not stay stale", rec.Key)
	}
	if strings.Count(notice.String(), "\n") != 1 || !strings.Contains(notice.String(), "/home/ada/aig-keys/abc.json") {
		t.Fatalf("notice = %q, want one line naming leftover file", notice)
	}
}

// stuckFile is a Store whose leftover record can be neither removed nor overwritten.
type stuckFile struct{ failingDelete }

func (stuckFile) Set(keystore.Record) (string, error) {
	return "", &fs.PathError{Op: "open", Path: "/home/ada/aig-keys/abc.json", Err: errors.New("read-only file system")}
}

func TestChainWriteFailsWhenStaleFileCanBeNeitherRemovedNorOverwritten(t *testing.T) {
	file := keystore.NewFile(t.TempDir())
	_, _ = file.Set(keystore.Record{BaseURL: gw, Key: "old", WrittenAt: t0})
	c := &keystore.Chain{Primary: newFakeKeyring(), Fallback: stuckFile{failingDelete{file}}, Notice: &strings.Builder{}}
	_, err := c.Set(keystore.Record{BaseURL: gw, Key: "new", WrittenAt: t0.Add(time.Hour)})
	if err == nil || !strings.Contains(err.Error(), "permission denied") || !strings.Contains(err.Error(), "read-only file system") {
		t.Fatalf("Set = %v, want both file errors: reads would keep serving the old key", err)
	}
}

func TestChainReadReportsKeychainErrorInsteadOfNoKey(t *testing.T) {
	c, kr, _, _ := newChain(t)
	kr.failGet = true
	_, err := c.Get(gw)
	if err == nil || errors.Is(err, keystore.ErrNotFound) || !strings.Contains(err.Error(), "keychain did not respond") || !strings.Contains(err.Error(), "aiven-llm login") {
		t.Fatalf("err = %v, want keychain error with login hint", err)
	}
}

func TestChainReadDoesNotFallBackPastBrokenKeyFile(t *testing.T) {
	c, kr, file, _ := newChain(t)
	_, _ = kr.Set(keystore.Record{BaseURL: gw, Key: "older", WrittenAt: t0})
	path, _ := file.Set(keystore.Record{BaseURL: gw, Key: "newer", WrittenAt: t0.Add(time.Hour)})
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rec, err := c.Get(gw); err == nil || errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("Get = %+v, %v; want key file error", rec, err)
	}
}

func TestChainDeleteReportsUnconfirmedKeychainRemoval(t *testing.T) {
	c, kr, file, _ := newChain(t)
	_, _ = file.Set(keystore.Record{BaseURL: gw, Key: "k", WrittenAt: t0})
	kr.failDelete, kr.failGet = true, true
	err := c.Delete(gw)
	if err == nil || !strings.Contains(err.Error(), "may still hold the key") || !strings.Contains(err.Error(), "AIVEN_LLM_KEYSTORE=file") {
		t.Fatalf("Delete = %v, want unconfirmed-removal error", err)
	}
	if _, err := file.Get(gw); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("file record survived: %v", err)
	}
}
