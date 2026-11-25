// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"strings"
	"testing"
)

// Hints on the launch and login paths come out in the short form, each through its own caller.
func TestErrorPathHintsUseShortName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*session) *session
		args  []string
		want  string
	}{
		{
			"harness missing",
			func(s *session) *session { s.runner.missing = []string{"claude"}; return s.loggedIn(keyValid) },
			[]string{"launch", "claude"},
			"install it, then run avn-aig setup claude",
		},
		{
			"setup missing",
			func(s *session) *session { return s.loggedIn(keyValid) },
			[]string{"launch", "claude"},
			"Run: avn-aig setup claude",
		},
		{
			"no key stored",
			func(s *session) *session { return s },
			[]string{"models"},
			"no key stored. Run: avn-aig login",
		},
		{
			"login next step",
			func(s *session) *session { return s.stdin(keyValid + "\n") },
			[]string{"login"},
			"avn-aig setup <harness>",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.setup(newSession(t, newGatewayDouble(t)))
			s.deps.Command = "avn-aig"
			r := s.run(tc.args...)
			out := r.stdout + r.stderr
			if !strings.Contains(out, tc.want) || strings.Contains(out, "aiven-ai-gateway setup") ||
				strings.Contains(out, "aiven-ai-gateway login") {
				t.Errorf("exit %d, output %q, want %q", r.code, out, tc.want)
			}
		})
	}
}
