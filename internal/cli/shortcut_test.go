// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/cli"
)

// avn-aig <harness> [native args] is launch <harness> [native args]; flags after the name belong to the tool.
func TestHarnessNameLaunchesLikeLaunch(t *testing.T) {
	g := newGatewayDouble(t)
	for _, tool := range []string{"claude", "codex", "pi", "opencode", "omp"} {
		for _, native := range [][]string{nil, {"--plain"}, {"--help"}, {"--", "resume"}, {"two words", ""}} {
			long, short := newSession(t, g).loggedIn(keyValid), newSession(t, g).loggedIn(keyValid)
			for _, s := range []*session{long, short} {
				if r := s.run("setup", tool, "--yes"); r.code != 0 {
					t.Fatalf("%s setup: %s", tool, r.stderr)
				}
			}
			if r := long.run(append([]string{"launch", tool}, native...)...); r.code != 0 {
				t.Fatalf("launch %s %q: %s", tool, native, r.stderr)
			}
			if r := short.run(append([]string{tool}, native...)...); r.code != 0 {
				t.Fatalf("%s %q: %s", tool, native, r.stderr)
			}
			argv := func(s *session) string {
				return strings.ReplaceAll(strings.Join(s.launched().argv, "\x00"), s.fakeHome, "HOME")
			}
			if got, want := argv(short), argv(long); got != want {
				t.Errorf("%s %q: argv %q, want %q", tool, native, got, want)
			}
		}
	}
}

// In a terminal the bare command offers the installed tools; Enter launches nothing.
func TestBareCommandOffersInstalledHarnesses(t *testing.T) {
	s := newSession(t, newGatewayDouble(t))
	s.deps.Prompter = &scriptedPrompter{answers: []string{""}}
	r := s.run()
	if r.code != 0 || !strings.Contains(r.stderr, "Nothing launched.") ||
		strings.Contains(r.stdout, "usage:") ||
		len(s.runner.calls) != 0 {
		t.Fatalf(
			"exit %d, stdout %q, stderr %q, calls %d",
			r.code,
			r.stdout,
			r.stderr,
			len(s.runner.calls),
		)
	}
}

func TestHelpLeadsWithInvokedCommand(t *testing.T) {
	var out bytes.Buffer
	cli.Informational([]string{"--help"}, nil, cli.Deps{Stdout: &out, Command: "avn-aig"})
	if !strings.HasPrefix(
		out.String(),
		"usage: avn-aig <command> [args]\n\nCommands\n  <harness> [native args]\n",
	) {
		t.Errorf("help starts %q", out.String()[:min(out.Len(), 120)])
	}
}
