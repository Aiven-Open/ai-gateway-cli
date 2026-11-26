// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
)

// A new key for a tool that is already up to date is saved: the tool reads the key file, so keeping the old key would
// leave it on a key the user replaced.
func TestSetupSavesRotatedKeyWhenNothingElseChanges(t *testing.T) {
	s := newSession(t, newGatewayDouble(t))
	if r := s.stdin(keyShort).run("setup", "--key-stdin", "--yes", "claude"); r.code != 0 {
		t.Fatalf("first setup: exit %d, stderr %q", r.code, r.stderr)
	}
	r := s.stdin(keyBare).run("setup", "--key-stdin", "--yes", "claude")
	if r.code != 0 || !strings.Contains(r.stderr, "Already configured; no file needs a change.") {
		t.Fatalf("rotation: exit %d, stderr %q", r.code, r.stderr)
	}
	if got := readOrEmpty(t, s.keyPath()); got != keyBare+"\n" {
		t.Errorf("key file = %q, want %q", got, keyBare+"\n")
	}
	if !strings.Contains(r.stderr, "saved to "+s.keyPath()) {
		t.Errorf("summary does not report the save: %q", r.stderr)
	}
	// The same key again writes nothing.
	r = s.stdin(keyBare).run("setup", "--key-stdin", "--yes", "claude")
	if r.code != 0 || strings.Contains(r.stderr, "Saving the key") ||
		!strings.Contains(r.stderr, "unchanged; existing key files unchanged") {
		t.Errorf("same key: exit %d, stderr %q", r.code, r.stderr)
	}
}

// A tool whose checks failed may still need the old key, so the new one is not saved beside an up-to-date tool.
func TestSetupKeepsOldKeyWhenAnotherToolFailsItsChecks(t *testing.T) {
	g := newGatewayDouble(t)
	g.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet"},{"id":"gpt-6.1-sol"}]}`))
		case r.URL.Path == "/v1/chat/completions":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"chat failed"}}`))
		default:
			_, _ = w.Write([]byte(`{"type":"message","object":"response","content":[],"output":[]}`))
		}
	})
	s := newSession(t, g)
	if r := s.stdin(keyShort).run("setup", "--key-stdin", "--yes", "claude"); r.code != 0 {
		t.Fatalf("first setup: exit %d, stderr %q", r.code, r.stderr)
	}
	s.deps.Prompter = &scriptedPrompter{answers: []string{"claude,opencode"}}
	r := s.stdin(keyBare).run("setup", "--key-stdin", "--yes")
	if got := readOrEmpty(t, s.keyPath()); got != keyShort+"\n" {
		t.Errorf("key file = %q, want %q; exit %d, stderr %q", got, keyShort+"\n", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "not saved by this run") {
		t.Errorf("summary does not say the key was not saved: %q", r.stderr)
	}
}

// An up-to-date tool reads the key file, so a failed write beside it keeps the new key.
func TestSetupKeepsNewKeyWhenUnchangedToolSitsBesideFailedWrite(t *testing.T) {
	s := newSession(t, newGatewayDouble(t))
	if r := s.stdin(keyShort).run("setup", "--key-stdin", "--yes", "claude"); r.code != 0 {
		t.Fatalf("first setup: exit %d, stderr %q", r.code, r.stderr)
	}
	s.deps.WriteFile = failNth(filepath.Join(s.fakeHome, ".codex", "config.toml"), 1)
	s.deps.Prompter = &scriptedPrompter{answers: []string{"claude,codex"}}
	r := s.stdin(keyBare).run("setup", "--key-stdin", "--yes")
	if r.code != 1 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if got := readOrEmpty(t, s.keyPath()); got != keyBare+"\n" {
		t.Errorf("key file = %q, want %q", got, keyBare+"\n")
	}
}

// A run that configures no tool leaves the key file as it was before the run.
func TestSetupPutsPreviousKeyBackWhenNoToolIsConfigured(t *testing.T) {
	for name, saved := range map[string]string{"replacing a key": keyFlash, "first key": ""} {
		t.Run(name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t))
			if saved != "" {
				s.loggedIn(saved)
			}
			s.deps.WriteFile = failNth(filepath.Join(s.fakeHome, claude.SettingsFile), 1)
			r := s.stdin(keyValid).run("setup", "--key-stdin", "--yes", "claude")
			if r.code != 1 {
				t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
			}
			want, note := "", "the new key was removed because no tool was configured"
			if saved != "" {
				want = saved + "\n"
				note = "the previous key was put back because no tool was configured"
			}
			if got := readOrEmpty(t, s.keyPath()); got != want {
				t.Errorf("key file = %q, want %q", got, want)
			}
			if !strings.Contains(r.stderr, "not saved by this run; "+note) {
				t.Errorf("summary does not say what happened to the key: %q", r.stderr)
			}
		})
	}
}

// Rollback touches the key file only while it still holds the key this run saved.
func TestSetupRollbackLeavesKeyFileAnotherProcessChanged(t *testing.T) {
	for name, change := range map[string]func(path string){
		"another login": func(path string) { _ = fsutil.WriteAtomic(path, []byte(keyShort+"\n"), 0o600) },
		"a logout":      func(path string) { _ = os.Remove(path) },
	} {
		t.Run(name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyFlash)
			fail := filepath.Join(s.fakeHome, claude.SettingsFile)
			s.deps.WriteFile = func(path string, data []byte, perm os.FileMode) error {
				if path == fail {
					change(s.keyPath())
					return errors.New("disk full")
				}
				return fsutil.WriteAtomic(path, data, perm)
			}
			r := s.stdin(keyValid).run("setup", "--key-stdin", "--yes", "claude")
			want := keyShort + "\n"
			if name == "a logout" {
				want = ""
			}
			if got := readOrEmpty(t, s.keyPath()); r.code != 1 || got != want {
				t.Errorf("exit %d, key file = %q, want %q", r.code, got, want)
			}
			if !strings.Contains(r.stderr, "the key file changed during setup so it was left alone") {
				t.Errorf("summary does not say the key file was left alone: %q", r.stderr)
			}
		})
	}
}

// A tool that wrote a file keeps the new key, so the file it wrote can still be used.
func TestSetupKeepsNewKeyAfterPartialWrite(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyFlash)
	s.deps.WriteFile = failNth(filepath.Join(s.fakeHome, ".codex", "aiven.config.toml"), 1)
	r := s.stdin(keyValid).run("setup", "--key-stdin", "--yes", "codex")
	if r.code != 1 || !strings.Contains(r.stderr, "partially configured") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if got := readOrEmpty(t, s.keyPath()); got != keyValid+"\n" {
		t.Errorf("key file = %q, want %q", got, keyValid+"\n")
	}
}

// A rotation that cannot be saved is a failed run, not a completed one.
func TestSetupFailedRotationIsNotReportedAsComplete(t *testing.T) {
	s := newSession(t, newGatewayDouble(t))
	if r := s.stdin(keyShort).run("setup", "--key-stdin", "--yes", "claude"); r.code != 0 {
		t.Fatalf("first setup: exit %d, stderr %q", r.code, r.stderr)
	}
	s.deps.WriteFile = failNth(s.keyPath(), 1)
	r := s.stdin(keyBare).run("setup", "--key-stdin", "--yes", "claude")
	if r.code != 1 || strings.Contains(r.stderr, "Setup complete") ||
		strings.Contains(r.stderr, "Start a session") ||
		!strings.Contains(r.stderr, "Setup finished with errors") {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
}

// A missing tool is not up to date, so a new key is not saved beside the one that is.
func TestSetupKeepsOldKeyWhenAnotherToolIsNotInstalled(t *testing.T) {
	s := newSession(t, newGatewayDouble(t))
	if r := s.stdin(keyShort).run("setup", "--key-stdin", "--yes", "claude"); r.code != 0 {
		t.Fatalf("first setup: exit %d, stderr %q", r.code, r.stderr)
	}
	s.runner.missing = []string{"codex"}
	s.deps.Prompter = &scriptedPrompter{answers: []string{"claude,codex"}}
	r := s.stdin(keyBare).run("setup", "--key-stdin", "--yes")
	if got := readOrEmpty(t, s.keyPath()); got != keyShort+"\n" {
		t.Errorf("key file = %q, want %q; exit %d, stderr %q", got, keyShort+"\n", r.code, r.stderr)
	}
}

// An up-to-date tool is not ready when the key it was checked with was not saved and none is usable.
func TestSetupDoesNotAdvertiseUnchangedToolWhenKeyWasNotSaved(t *testing.T) {
	s := newSession(t, newGatewayDouble(t))
	if r := s.stdin(keyShort).run("setup", "--key-stdin", "--yes", "claude"); r.code != 0 {
		t.Fatalf("first setup: exit %d, stderr %q", r.code, r.stderr)
	}
	if err := os.Remove(s.keyPath()); err != nil {
		t.Fatal(err)
	}
	s.runner.missing = []string{"codex"}
	s.deps.Prompter = &scriptedPrompter{answers: []string{"claude,codex"}}
	r := s.stdin(keyBare).run("setup", "--key-stdin", "--yes")
	if strings.Contains(r.stderr, "aiven-ai-gateway claude\n") ||
		!strings.Contains(r.stderr, "the key was not saved") {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
	if got := readOrEmpty(t, s.keyPath()); got != "" {
		t.Errorf("key file = %q, want none", got)
	}
}
