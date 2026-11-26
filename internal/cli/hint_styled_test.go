// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/cli"
)

// ansiCodes is written apart from the production styling code so that a change there cannot hide a code in the test.
var ansiCodes = regexp.MustCompile("\x1b\\[[0-9;]*m")

// Next-step commands keep the form the person typed on a styled terminal, where the command is also coloured.
func TestStyledHintsUseInvokedForm(t *testing.T) {
	for _, cmd := range []string{"avn-aig", "npx @aivenio/ai-gateway"} {
		for _, tc := range []struct {
			name    string
			prepare func(*session)
			args    []string
			want    string
		}{
			{"start a session", func(*session) {}, []string{"setup", "claude", "--yes"}, "\nStart a session\n  %s claude\n"},
			{
				"setup again after a decline",
				func(s *session) { s.deps.Prompter = &answerPrompter{answer: "n"} },
				[]string{"setup", "codex"},
				"\nConfigure later\n  %s setup codex\n",
			},
			{
				"setup again after an error",
				func(s *session) { s.runner.missing = []string{"codex"} },
				[]string{"setup", "codex", "--yes"},
				"\nNeeds attention\n  %s setup codex\n",
			},
			{"login next step", func(s *session) { s.stdin(keyValid + "\n") }, []string{"login"}, "\n    %s setup <harness>\n"},
		} {
			t.Run(tc.name+" as "+cmd, func(t *testing.T) {
				s := newSession(t, newGatewayDouble(t))
				if tc.args[0] != "login" {
					s.loggedIn(keyValid)
				}
				s.deps.Command = cmd
				s.deps.Term = cli.Terminal{StdoutTTY: true, StderrTTY: true}
				tc.prepare(s)
				r := s.run(tc.args...)
				raw := r.stdout + r.stderr
				if !strings.Contains(raw, "\x1b[") {
					t.Fatalf("output is not styled: %q", raw)
				}
				out := ansiCodes.ReplaceAllString(raw, "")
				if want := strings.Replace(tc.want, "%s", cmd, 1); !strings.Contains(out, want) {
					t.Errorf("exit %d, output %q, want %q", r.code, out, want)
				}
				if strings.Contains(out, "aiven-ai-gateway ") {
					t.Errorf("output %q still names the long command", out)
				}
			})
		}
	}
}
