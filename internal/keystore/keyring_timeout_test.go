// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package keystore

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// blockedKeychain never answers, as a locked Secret Service with an unanswered unlock prompt.
type blockedKeychain struct{ release chan struct{} }

func (b blockedKeychain) Get(string, string) (string, error) { <-b.release; return "", nil }
func (b blockedKeychain) Set(string, string, string) error   { <-b.release; return nil }
func (b blockedKeychain) Delete(string, string) error        { <-b.release; return nil }

func blockedKeyring(t *testing.T) *Keyring {
	t.Helper()
	b := blockedKeychain{release: make(chan struct{})}
	t.Cleanup(func() { close(b.release) })
	return &Keyring{kc: b, timeout: 20 * time.Millisecond}
}

func TestBlockedKeychainFailsInsteadOfHanging(t *testing.T) {
	k := blockedKeyring(t)
	done := make(chan error, 3)
	go func() { _, err := k.Get("https://a.example"); done <- err }()
	go func() { _, err := k.Set(Record{BaseURL: "https://a.example", Key: "k"}); done <- err }()
	go func() { done <- k.Delete("https://a.example") }()
	for range 3 {
		select {
		case err := <-done:
			if err == nil || errors.Is(err, ErrNotFound) {
				t.Errorf("err = %v, want keychain failure", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("keychain call still blocked")
		}
	}
}

func TestChainFallsBackToFileWhenKeychainBlocks(t *testing.T) {
	notice := &strings.Builder{}
	file := NewFile(t.TempDir())
	c := &Chain{Primary: blockedKeyring(t), Fallback: file, Notice: notice}
	if _, err := c.Set(Record{BaseURL: "https://a.example", Key: "k"}); err != nil {
		t.Fatal(err)
	}
	if rec, err := c.Get("https://a.example"); err != nil || rec.Key != "k" {
		t.Fatalf("Get = %+v, %v; want file record", rec, err)
	}
	if strings.Count(notice.String(), "\n") != 1 {
		t.Fatalf("notice = %q, want one line", notice)
	}
}
