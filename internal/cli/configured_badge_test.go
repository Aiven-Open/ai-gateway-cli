// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
)

// A saved launcher preference does not make a tool with no Aiven config files read as configured, yet its model
// is still offered as Keep current.
// Mutant: toolBadge returns configured for any present, granted existing model whatever the tool.
func TestSavedPreferenceAloneLeavesBadgeNew(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyProduction)
	s.runner.missing = []string{"opencode", "codex", "claude", "omp"}
	prefs := map[string]string{"pi": "deepseek-v3.2"}
	if err := config.SaveModelPrefs(s.fakeHome, s.env.BaseURL, prefs, nil); err != nil {
		t.Fatal(err)
	}
	s.deps.Prompter = &scriptedPrompter{answers: []string{"pi", "", "n", "1"}}
	r := s.run("setup", "--plain")
	if !strings.Contains(r.stderr, "Pi (pi) — new\n") ||
		strings.Contains(r.stderr, "Pi (pi) — configured") {
		t.Errorf("badge is not new: %q", r.stderr)
	}
	if !strings.Contains(r.stderr, "Keep current (deepseek-v3.2)") {
		t.Errorf("Keep current not offered: %q", r.stderr)
	}
}
