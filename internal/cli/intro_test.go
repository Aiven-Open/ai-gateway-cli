// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/cli"
)

const intro = "Aiven AI Gateway setup\n" +
	"Connects your coding tools to Aiven AI Gateway. Checks and saves your key,\n" +
	"then adds an Aiven profile to each tool. No tool settings change until you apply.\n" +
	"Source: https://github.com/Aiven-Open/ai-gateway-cli\n\n"

func TestSetupStartsWithIntro(t *testing.T) {
	for _, tc := range []struct {
		name string
		term cli.Terminal
		args []string
	}{
		{"plain", cli.Terminal{}, []string{"setup", "claude", "--yes"}},
		{"styled", cli.Terminal{StderrTTY: true}, []string{"setup", "claude", "--yes"}},
		{"failing key", cli.Terminal{}, []string{"setup", "claude", "--key-stdin", "--yes"}},
	} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).stdin("sk-test-unknown\n")
		s.deps.Term = tc.term
		r := s.run(tc.args...)
		if got := escapes.ReplaceAllString(r.stderr, ""); !strings.HasPrefix(got, intro) {
			t.Errorf("%s: stderr starts %q", tc.name, got[:min(len(got), 200)])
		}
	}
}
