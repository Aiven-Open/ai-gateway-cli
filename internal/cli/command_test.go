// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"bytes"
	"cmp"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/cli"
)

func TestCommandNameFollowsInvocation(t *testing.T) {
	for _, tc := range []struct {
		argv0   string
		environ []string
		want    string
	}{
		{"/opt/homebrew/bin/avn-aig", nil, "avn-aig"},
		{`C:\npm\AVN-AIG.EXE`, nil, "avn-aig"},
		{"/Users/x/go/bin/aiven-ai-gateway", nil, "aiven-ai-gateway"},
		{"avn-aig-old", nil, "aiven-ai-gateway"},
		{"/n/bin/aiven-ai-gateway", []string{cli.InvokedAsVar + "=avn-aig"}, "avn-aig"},
		{"/n/bin/aiven-ai-gateway", []string{cli.InvokedAsVar + "=npx"}, "npx @aivenio/ai-gateway"},
		{"/n/bin/avn-aig", []string{cli.InvokedAsVar + "=rm -rf ~"}, "avn-aig"},
		{"/n/bin/avn-aig", []string{cli.InvokedAsVar + "=aiven-ai-gateway"}, "aiven-ai-gateway"},
	} {
		if got := cli.CommandName(tc.argv0, tc.environ); got != tc.want {
			t.Errorf("CommandName(%q, %q) = %q, want %q", tc.argv0, tc.environ, got, tc.want)
		}
	}
}

func TestHintsUseInvokedCommand(t *testing.T) {
	for _, command := range []string{"", "avn-aig", "npx @aivenio/ai-gateway"} {
		want := cmp.Or(command, "aiven-ai-gateway")
		var out, errb bytes.Buffer
		cli.Informational(
			[]string{"frobnicate"},
			nil,
			cli.Deps{Stdout: &out, Stderr: &errb, Command: command},
		)
		if got := errb.String(); got != "aiven-ai-gateway: unknown command. Run: "+want+" --help\n" {
			t.Errorf("%q: stderr %q", command, got)
		}
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
		s.deps.Command = command
		s.runner.missing = []string{"claude"}
		r := s.run("setup", "claude", "--yes")
		if !strings.Contains(r.stderr, "install it, then run "+want+" setup claude") ||
			!strings.Contains(r.stderr, "\n  "+want+" setup claude\n") ||
			command != "" && strings.Contains(r.stderr, "aiven-ai-gateway setup") {
			t.Errorf("%q: stderr %q", command, r.stderr)
		}
	}
}

// A session started through npx must not tell a launcher run inside it to print npx hints.
func TestLaunchKeepsInvocationOutOfSession(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.env.Environ = append(s.env.Environ, cli.InvokedAsVar+"=npx")
	if r := s.setupAndLaunch("codex"); r.code != 0 {
		t.Fatalf("launch: %s", r.stderr)
	}
	if _, found := s.launched().envVar(cli.InvokedAsVar); found {
		t.Errorf("%s reached the session", cli.InvokedAsVar)
	}
}
