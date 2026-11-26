// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"slices"
	"testing"
)

// A management command must reach the tool first, as typed: a model flag in front turns it into a prompt.
func TestNativeManagementCommandsGetNoModel(t *testing.T) {
	for tool, commands := range map[string][][]string{
		"pi":       {{"uninstall", "npm:x"}, {"remove", "npm:x"}, {"list"}},
		"opencode": {{"providers", "list"}, {"auth", "list"}, {"plugin", "x"}, {"plug", "x"}, {"models"}},
		"omp":      {{"models"}, {"stats"}, {"login"}, {"setup"}, {"worktree", "list"}},
	} {
		for _, native := range commands {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
			if r := s.setupAndLaunch(tool, native...); r.code != 0 {
				t.Fatalf("%s %q: %s", tool, native, r.stderr)
			}
			if got, want := s.launched().argv, append([]string{tool}, native...); !slices.Equal(got, want) {
				t.Errorf("%s %q: argv %q, want %q", tool, native, got, want)
			}
		}
	}
}
