// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
)

// A broken preference file stops launch before it picks another model, and setup before any check
// or write. Mutant: let LoadModelPrefs read null as no preferences.
func TestLaunchAndSetupRefuseMalformedPreferences(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	if r := s.run("setup", "pi", "--yes"); r.code != 0 {
		t.Fatalf("setup: %s", r.stderr)
	}
	path := config.ModelPrefsPath(s.fakeHome)
	writeFile(t, path, "null")
	r := s.run("launch", "pi")
	if r.code != 1 || len(s.runner.calls) != 0 || !strings.Contains(r.stderr, path) {
		t.Fatalf("launch: exit %d, calls %d, stderr %q", r.code, len(s.runner.calls), r.stderr)
	}
	before := len(g.journal())
	models := filepath.Join(s.fakeHome, ".pi/agent/models.json")
	original, _ := os.ReadFile(models)
	r = s.run("setup", "pi", "--yes")
	for _, req := range g.journal()[before:] {
		if req.Method != "GET" {
			t.Errorf("setup sent %s %s despite the broken file", req.Method, req.URL.Path)
		}
	}
	if after, _ := os.ReadFile(models); r.code != 1 || string(after) != string(original) ||
		readOrEmpty(t, path) != "null" {
		t.Fatalf("setup: exit %d, stderr %q", r.code, r.stderr)
	}
}
