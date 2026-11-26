// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"slices"
	"strings"
	"testing"
)

// Copying a whole line under Start a session must run the launcher with no extra argument: a model id after the
// harness name would reach the native tool, and Claude Code would take it as a prompt.
func TestStartSessionLinesCarryNoModelArgument(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	r := s.run("setup", "claude", "--yes")
	_, after, found := strings.Cut(r.stderr, "\nStart a session\n")
	if r.code != 0 || !found {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	lines := strings.Split(after, "\n")
	if got := strings.Fields(
		lines[0],
	); !slices.Equal(
		got,
		[]string{"aiven-ai-gateway", "launch", "claude"},
	) {
		t.Errorf("copied line %q runs %q", lines[0], got)
	}
	// keyValid's catalogue lists claude-sonnet, which the Anthropic preset makes the default.
	if want := "    Default model: claude-sonnet"; lines[1] != want {
		t.Errorf("model line %q, want %q", lines[1], want)
	}
}
