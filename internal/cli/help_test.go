// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/cli"
)

// plainHelp is the help as a pipe, a file or a plain terminal receives it.
const plainHelp = `usage: aiven-ai-gateway <command> [args]

Commands
  setup [harness] [--key-stdin] [-y|--yes] [--plain]
      Check a key, save it, then preview and approve each tool's configuration.
      List models; send one minimal request per configured protocol and model.
      These basic checks may incur usage; they do not test native tool requests.
      Never starts a session. Without a harness, a terminal offers a list of tools.
      At each preview: Enter or y applies, n declines that tool, v shows the full
      redacted diff and asks again. Enter and --yes keep native defaults unchanged.
  launch [harness] [--plain] [-- native args]
      Start one configured tool. Setup is required first; launch writes no
      configuration. Without a harness, a terminal offers one installed tool.
  login
      Check that a key can list models, then save it. Sends no test request and
      configures no tool.
  logout
      Remove the key saved for the configured gateway; other gateways' keys stay.
      Does not revoke a server key or unset inherited environment variables.
  models
      List your models by publisher, with exact ids for harness flags.

  harness: claude | codex | pi | opencode | omp

Output
  Help and the model list go to stdout. Prompts, progress, previews, errors and
  next steps go to stderr. --plain, NO_COLOR or TERM=dumb use numbered prompts
  with no colour or cursor motion. Help needs no credentials or configuration.
  --version prints build metadata without reading credentials or configuration.
  Native arguments must follow -- and are forwarded literally.
`

var escapes = regexp.MustCompile("\x1b\\[[0-9;]*m")

// Help is styled only when stdout is a terminal and plain text was not asked for; stderr's capability never decides
// it. Every form carries the same words.
func TestHelpIsStyledOnlyOnAStdoutTerminal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		term    cli.Terminal
		environ []string
		styled  bool
	}{
		{name: "redirected", term: cli.Terminal{StdinTTY: true, StderrTTY: true}},
		{name: "stdout terminal", term: cli.Terminal{StdoutTTY: true}, styled: true},
		{name: "NO_COLOR", term: cli.Terminal{StdoutTTY: true}, environ: []string{"NO_COLOR=1"}},
		{name: "TERM=dumb", term: cli.Terminal{StdoutTTY: true}, environ: []string{"TERM=dumb"}},
		{name: "empty NO_COLOR", term: cli.Terminal{StdoutTTY: true}, environ: []string{"NO_COLOR="}, styled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, args := range [][]string{nil, {"--help"}, {"setup", "--help"}} {
				var out, errb strings.Builder
				handled, code := cli.Informational(args, tc.environ, cli.Deps{Stdout: &out, Stderr: &errb, Term: tc.term})
				if !handled || code != 0 || errb.Len() != 0 {
					t.Fatalf("%q: handled %v, exit %d, stderr %q", args, handled, code, errb.String())
				}
				got := out.String()
				if plain := escapes.ReplaceAllString(got, ""); plain != plainHelp {
					t.Errorf("%q: words differ from the plain help:\n%s", args, plain)
				}
				if tc.styled {
					for _, want := range []string{"\x1b[1mCommands\x1b[0m", "  \x1b[1;36msetup\x1b[0m [harness]", "\x1b[1musage:\x1b[0m"} {
						if !strings.Contains(got, want) {
							t.Errorf("%q: styled help lacks %q: %q", args, want, got)
						}
					}
				} else if got != plainHelp {
					t.Errorf("%q: help has escapes: %q", args, got)
				}
			}
		})
	}
}
