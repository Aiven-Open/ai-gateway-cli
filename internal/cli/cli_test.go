// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/cli"
	"github.com/aiven-open/ai-gateway-cli/internal/config"
)

type result struct {
	code           int
	stdout, stderr string
}

func run(t *testing.T, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	deps := cli.Deps{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb}
	code := cli.Run(context.Background(), args, config.Env{BaseURL: "https://gw.test"}, deps)
	return result{code, out.String(), errb.String()}
}

func TestHelpExitsZero(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			r := run(t, arg)
			if r.code != 0 {
				t.Fatalf("exit code = %d, want 0", r.code)
			}
			if !strings.HasPrefix(r.stdout, "usage: aiven-ai-gateway <command> [args]\n") {
				t.Fatalf("stdout = %q, want usage", r.stdout)
			}
		})
	}
}

func TestNoArgsPrintsHelpExitsZero(t *testing.T) {
	r := run(t)
	if r.code != 0 {
		t.Fatalf("exit code = %d, want 0", r.code)
	}
	if !strings.HasPrefix(r.stdout, "usage: aiven-ai-gateway <command> [args]\n") {
		t.Fatalf("stdout = %q, want usage", r.stdout)
	}
	if r.stderr != "" {
		t.Fatalf("stderr = %q, want empty", r.stderr)
	}
}

func TestUnknownCommand(t *testing.T) {
	r := run(t, "frobnicate", "--x")
	if r.code != 1 {
		t.Fatalf("exit code = %d, want 1", r.code)
	}
	if r.stderr != "aiven-ai-gateway: unknown command. Run: aiven-ai-gateway --help\n" {
		t.Fatalf("stderr = %q", r.stderr)
	}
	if r.stdout != "" {
		t.Fatalf("stdout = %q, want empty", r.stdout)
	}
}

func TestUsageListsEveryCommand(t *testing.T) {
	r := run(t, "help")
	for _, cmd := range []string{"login", "setup", "launch", "models", "logout", "claude", "omp", "codex", "pi", "opencode"} {
		if !strings.Contains(r.stdout, cmd) {
			t.Errorf("usage lacks %q:\n%s", cmd, r.stdout)
		}
	}
}

func TestVersionPrintsBuildInfo(t *testing.T) {
	for _, arg := range []string{"version", "--version"} {
		t.Run(arg, func(t *testing.T) {
			var out bytes.Buffer
			deps := cli.Deps{Stdout: &out, Build: cli.Build{Version: "0.1.0-rc.1", Commit: "abc1234", Date: "2026-10-15T18:08:57Z"}}
			code := cli.Run(context.Background(), []string{arg}, config.Env{}, deps)
			want := "aiven-ai-gateway 0.1.0-rc.1 (commit abc1234, built 2026-10-15T18:08:57Z)\n"
			if code != 0 || out.String() != want {
				t.Fatalf("code = %d, stdout = %q, want 0, %q", code, out.String(), want)
			}
		})
	}
}

// Arguments the launcher does not take are refused before configuration or credentials are read, without echoing them.
func TestMalformedInvocationsAreRefusedWithoutEcho(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"login", "sk-pasted"}, "login: unexpected argument. Run: aiven-ai-gateway --help"},
		{[]string{"logout", "now"}, "logout: unexpected argument. Run: aiven-ai-gateway --help"},
		{[]string{"models", "--all"}, "models: unexpected argument. Run: aiven-ai-gateway --help"},
		{[]string{"launch", "pi", "codex"}, "launch: name one harness and put native arguments after --. Run: aiven-ai-gateway launch --help"},
		{[]string{"launch", "pi", "--model", "x"}, "launch: name one harness and put native arguments after --. Run: aiven-ai-gateway launch --help"},
		{[]string{"launch"}, "launch: a harness is required without a terminal"},
		{[]string{"setup", "pi", "codex"}, "setup: unexpected argument. Run: aiven-ai-gateway setup --help"},
		{[]string{"setup", "--key=sk-pasted"}, "do not pass API keys on the command line; use --key-stdin or interactive prompt"},
		{[]string{"setup", "--key", "sk-pasted"}, "do not pass API keys on the command line; use --key-stdin or interactive prompt"},
		{[]string{"setup"}, "setup: a harness is required without a terminal; use login for credential-only input"},
		{[]string{"sk-pasted"}, "unknown command. Run: aiven-ai-gateway --help"},
		{[]string{"connect", "claude", "sk-pasted"}, "connect was removed. Run: aiven-ai-gateway setup <harness>"},
		{[]string{"omp", "init", "-h"}, "omp init was removed; existing project files are left as they are. Run: aiven-ai-gateway setup omp"},
		{[]string{"claude", "--help"}, "session shortcut removed. Run: aiven-ai-gateway launch claude"},
		{[]string{"codex", "sk-pasted"}, "session shortcut removed. Run: aiven-ai-gateway launch codex"},
	} {
		var out, errb bytes.Buffer
		handled, code := cli.Informational(tc.args, nil, cli.Deps{Stdout: &out, Stderr: &errb})
		if !handled || code != 1 || out.Len() != 0 || errb.String() != "aiven-ai-gateway: "+tc.want+"\n" {
			t.Errorf("%q: handled %v, exit %d, stdout %q, stderr %q", tc.args, handled, code, out.String(), errb.String())
		}
	}
	for _, args := range [][]string{{"--version"}, {"version"}, {"launch", "pi", "--", "--help"}, {"setup", "pi"}, {"login"}} {
		var out, errb bytes.Buffer
		if handled, _ := cli.Informational(args, nil, cli.Deps{Stdout: &out, Stderr: &errb}); handled || out.Len()+errb.Len() != 0 {
			t.Errorf("%q: handled before dispatch: stdout %q, stderr %q", args, out.String(), errb.String())
		}
	}
}
