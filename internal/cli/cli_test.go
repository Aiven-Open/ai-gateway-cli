// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Aiven-Open/ai-gateway-cli/internal/cli"
	"github.com/Aiven-Open/ai-gateway-cli/internal/config"
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
			if !strings.HasPrefix(r.stdout, "usage: aiven-llm <command> [args]\n") {
				t.Fatalf("stdout = %q, want usage", r.stdout)
			}
		})
	}
}

func TestNoArgsPrintsUsageExitsOne(t *testing.T) {
	r := run(t)
	if r.code != 1 {
		t.Fatalf("exit code = %d, want 1", r.code)
	}
	if !strings.HasPrefix(r.stdout, "usage: aiven-llm <command> [args]\n") {
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
	if r.stderr != "aiven-llm: unknown command: frobnicate\n" {
		t.Fatalf("stderr = %q", r.stderr)
	}
	if r.stdout != "" {
		t.Fatalf("stdout = %q, want empty", r.stdout)
	}
}

func TestUsageListsEveryCommand(t *testing.T) {
	r := run(t, "help")
	for _, cmd := range []string{"login", "models", "logout", "claude", "omp init", "codex|pi [--isolated]", "opencode"} {
		if !strings.Contains(r.stdout, cmd) {
			t.Errorf("usage lacks %q:\n%s", cmd, r.stdout)
		}
	}
}
