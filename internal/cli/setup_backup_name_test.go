// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

func TestSetupKeepsThePreviewedBackupNameWhenAnotherRunTakesIt(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	path := filepath.Join(s.fakeHome, ".codex/config.toml")
	const before = "model = 'o3'\n"
	writeFile(t, path, before)
	taken := fsutil.BackupPath(path, fixedNow)
	s.deps.Prompter = &hookPrompter{
		on:    "Apply option for",
		first: "v",
		hook:  func() { writeFile(t, taken, "another run") },
	}
	r := s.run("setup", "--key-stdin", "codex")
	if r.code != 1 || !strings.Contains(r.stderr, "(previous version: "+taken+")") ||
		!strings.Contains(r.stderr, taken+" already exists; retry") {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if got, _ := os.ReadFile(path); string(got) != before {
		t.Errorf("config.toml = %q, want it untouched", got)
	}
}
