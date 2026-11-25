// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/cli"
	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

// busy is the refusal a writer gives while another run holds the lock, written out as users read it.
const busy = "another aiven-ai-gateway run is changing the saved key; retry when it finishes"

// holdLock takes the state lock as another run would and makes s give up on it quickly.
func holdLock(t *testing.T, s *session) {
	t.Helper()
	unlock, err := config.LockState(s.fakeHome, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unlock)
	s.deps.LockWait = 20 * time.Millisecond
}

// Mutant: remove the lockState call from login, logout, commitApproved or commitNoWrites.
func TestKeyWritersWaitForStateLock(t *testing.T) {
	t.Run("login", func(t *testing.T) {
		s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
		holdLock(t, s)
		if r := s.run("login"); r.code != 1 || !strings.Contains(r.stderr, busy) ||
			readOrEmpty(t, s.keyPath()) != "" {
			t.Fatalf("exit %d, key %q, stderr %q", r.code, readOrEmpty(t, s.keyPath()), r.stderr)
		}
	})
	t.Run("logout", func(t *testing.T) {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
		holdLock(t, s)
		if r := s.run("logout"); r.code != 1 || !strings.Contains(r.stderr, busy) ||
			readOrEmpty(t, s.keyPath()) != keyValid+"\n" {
			t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
		}
	})
	t.Run("setup writes", func(t *testing.T) {
		s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
		holdLock(t, s)
		r := s.run("setup", "pi", "--key-stdin", "--yes")
		if r.code != 1 || !strings.Contains(r.stderr, busy) || readOrEmpty(t, s.keyPath()) != "" ||
			readOrEmpty(t, filepath.Join(s.fakeHome, ".pi/agent/models.json")) != "" {
			t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
		}
	})
	t.Run("setup rotation", func(t *testing.T) {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyFlash).configured("claude").stdin(keyValid)
		holdLock(t, s)
		r := s.run("setup", "claude", "--key-stdin", "--yes")
		if r.code != 1 || !strings.Contains(r.stderr, busy) ||
			readOrEmpty(t, s.keyPath()) != keyFlash+"\n" {
			t.Fatalf("exit %d, key %q, stderr %q", r.code, readOrEmpty(t, s.keyPath()), r.stderr)
		}
	})
}

// A login that lands while setup is putting the previous key back must not report success and then
// lose its key: it waits for setup's lock and is told to retry.
// Mutant: remove the lockState call from commitApproved or login.
func TestLoginDuringRestoreWaitsForSetup(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	s.deps.WriteFile = func(path string, data []byte, perm os.FileMode) error {
		if path == s.keyPath() {
			return fsutil.WriteAtomic(path, data, perm)
		}
		return errors.New("synthetic configuration write failure")
	}
	loginCode := -1
	var loginErr strings.Builder
	s.deps.Remove = func(path string) error {
		login := s.deps
		login.Stdin, login.Stdout, login.Stderr = strings.NewReader(keyFlash+"\n"), io.Discard, &loginErr
		login.WriteFile, login.Remove, login.LockWait = nil, nil, 20*time.Millisecond
		loginCode = cli.Run(context.Background(), []string{"login"}, s.env, login)
		return os.Remove(path)
	}
	r := s.run("setup", "pi", "--key-stdin", "--yes")
	if r.code != 1 || loginCode != 1 || !strings.Contains(loginErr.String(), busy) {
		t.Fatalf("setup exit %d, login exit %d %q, setup stderr %q",
			r.code, loginCode, loginErr.String(), r.stderr)
	}
	if got := readOrEmpty(t, s.keyPath()); got != "" {
		t.Errorf("key file %q after a run that configured nothing", got)
	}
}
