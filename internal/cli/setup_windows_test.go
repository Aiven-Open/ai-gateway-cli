// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsRedirectIgnoresCase(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	s.env.Environ = append(s.env.Environ, "codex_home="+filepath.Join(s.fakeHome, "elsewhere"))
	r := s.run("setup", "--yes", "--key-stdin", "codex")
	if r.code == 0 || !strings.Contains(r.stderr+r.stdout, "would not read") {
		t.Fatalf("Windows redirect not refused: exit %d: %s %s", r.code, r.stderr, r.stdout)
	}
}

func TestWindowsPiSetupRequiresGitBash(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).stdin(keyValid)
	s.runner.missing = []string{"bash.exe"}
	if r := s.run("setup", "--yes", "--key-stdin", "pi"); r.code != 0 {
		t.Fatalf("setup pi with ProgramFiles Git Bash failed: exit %d: %s", r.code, r.stderr)
	}

	g2 := newGatewayDouble(t)
	s2 := newSession(t, g2).stdin(keyValid)
	s2.env.Environ = []string{"USERPROFILE=" + s2.fakeHome}
	s2.runner.missing = []string{"bash.exe"}
	r := s2.run("setup", "--yes", "--key-stdin", "pi")
	if r.code != 1 || !strings.Contains(r.stderr, "Git Bash on Windows") || len(g2.journal()) != 0 {
		t.Fatalf("setup pi without Git Bash: exit %d, requests %d, stderr %q", r.code, len(g2.journal()), r.stderr)
	}
}
