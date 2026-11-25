// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// keyValid's catalogue gives the Anthropic preset: sonnet claude-sonnet, opus and fable claude-opus, haiku falls
// back to the main model. A fresh file turns discovery on.
func TestReviewSummaryListsClaudeMenu(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	r := s.run("setup", "claude", "--yes")
	want := "\nClaude Code\n" +
		"  Default model     claude-sonnet\n" +
		"  Also in /model    claude-opus\n" +
		"                    and every Claude model your key grants\n" +
		"  Default provider  not changed\n" +
		"  File              ~/.claude/aiven.settings.json\n"
	if r.code != 0 || !strings.Contains(r.stderr, want) {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestReviewSummaryNamesBackupAndProvider(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	writeFile(t, filepath.Join(s.fakeHome, ".pi/agent/models.json"), `{"providers":{}}`)
	// Enter takes the first preset and Aiven as the default; n cancels at review.
	s.deps.Prompter = &scriptedPrompter{answers: []string{"", "", "n"}}
	r := s.run("setup", "pi", "--plain")
	want := "\nPi\n" +
		"  Default model     claude-sonnet\n" +
		"  Default provider  Aiven\n" +
		"  Files             ~/.pi/agent/models.json\n" +
		"                    previous version kept as a backup beside it\n" +
		"                    ~/.pi/agent/settings.json\n" +
		"                    ~/.config/aiven/ai-gateway-models.json\n"
	if !strings.Contains(r.stderr, want) {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
}

// Unattended runs print the table, not a diff.
func TestYesRunPrintsSummaryWithoutDiff(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	r := s.run("setup", "pi", "--yes")
	if r.code != 0 || strings.Contains(r.stderr, "+++ ") ||
		!strings.Contains(r.stderr, "\nPi\n  Default model") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
}
