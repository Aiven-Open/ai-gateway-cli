// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package cli dispatches aiven-ai-gateway commands.
package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
)

// Run executes one invocation and returns the process exit code.
func Run(ctx context.Context, args []string, env config.Env, deps Deps) int {
	if handled, code := Informational(args, env.Environ, deps); handled {
		return code
	}
	switch args[0] {
	case "--version", "version":
		_, _ = fmt.Fprintf(deps.Stdout, "aiven-ai-gateway %s (commit %s, built %s)\n", deps.Build.Version, deps.Build.Commit, deps.Build.Date)
		return 0
	case "login":
		return login(ctx, env, deps)
	case "setup":
		return setup(ctx, args[1:], env, deps)
	case "launch":
		return launchCommand(ctx, args[1:], env, deps)
	case "models":
		return listModels(ctx, env, deps)
	case "logout":
		return logout(env, deps)
	default:
		return fail(deps, "unknown command. Run: aiven-ai-gateway --help")
	}
}

// Informational handles help and rejects malformed invocations before configuration, cwd or credential access.
// environ is read only for NO_COLOR and TERM.
func Informational(args, environ []string, deps Deps) (bool, int) {
	presentation, _, _ := splitNative(args)
	plain := slices.Contains(presentation, "--plain")
	if len(args) == 0 || slices.Contains([]string{"help", "-h", "--help"}, args[0]) {
		writeHelp(deps, environ, plain)
		return true, 0
	}
	head, _, _ := strings.Cut(args[0], "=")
	if head == "--key" || head == "--api-key" {
		return true, fail(deps, "do not pass API keys on the command line; use login or setup --key-stdin")
	}
	cmd := args[0]
	if cmd == "--version" || cmd == "version" {
		return false, 0
	}
	if cmd == "connect" {
		return true, fail(deps, "connect was removed. Run: aiven-ai-gateway setup <harness>")
	}
	if cmd == "omp" && len(args) > 1 && args[1] == "init" {
		return true, fail(deps, "omp init was removed; existing project files are left as they are. Run: aiven-ai-gateway setup omp")
	}
	if slices.Contains(toolNames, cmd) {
		return true, fail(deps, "session shortcut removed. Run: aiven-ai-gateway launch "+cmd)
	}
	if !slices.Contains([]string{"launch", "setup", "login", "logout", "models"}, cmd) {
		return true, fail(deps, "unknown command. Run: aiven-ai-gateway --help")
	}
	before, _, _ := splitNative(args[1:])
	for _, a := range before {
		if a == "--help" || a == "-h" {
			writeHelp(deps, environ, plain)
			return true, 0
		}
	}
	switch cmd {
	case "setup":
		if o, err := parseSetup(args[1:]); err != nil {
			return true, fail(deps, err.Error())
		} else if o.tool == "" && deps.Prompter == nil {
			return true, fail(deps, "setup: a harness is required without a terminal; use login for credential-only input")
		}
	case "launch":
		if o, err := parseLaunch(args[1:]); err != nil {
			return true, fail(deps, err.Error())
		} else if o.tool == "" && deps.Prompter == nil {
			return true, fail(deps, "launch: a harness is required without a terminal")
		}
	case "login", "logout", "models":
		if len(args) != 1 {
			return true, fail(deps, cmd+": unexpected argument. Run: aiven-ai-gateway --help")
		}
	}
	return false, 0
}

func splitNative(args []string) ([]string, []string, bool) {
	if i := slices.Index(args, "--"); i >= 0 {
		return args[:i], args[i+1:], true
	}
	return args, nil, false
}

// helpCommand is one entry of the help: the command and its arguments, then what it does.
type helpCommand struct {
	name, args string
	about      []string
}

var helpCommands = []helpCommand{
	{"setup", "[harness] [--key-stdin] [-y|--yes] [--plain]", []string{
		"Check a key, save it, then preview and approve each tool's configuration.",
		"List models; send one minimal request per configured protocol and model.",
		"These basic checks may incur usage; they do not test native tool requests.",
		"Never starts a session. Without a harness, a terminal offers a list of tools.",
		"At each preview: Enter or y applies, n declines that tool, v shows the full",
		"redacted diff and asks again. Enter and --yes keep native defaults unchanged.",
	}},
	{"launch", "[harness] [--plain] [-- native args]", []string{
		"Start one configured tool. Setup is required first; launch writes no",
		"configuration. Without a harness, a terminal offers one installed tool.",
	}},
	{"login", "", []string{
		"Check that a key can list models, then save it. Sends no test request and",
		"configures no tool.",
	}},
	{"logout", "", []string{
		"Remove the key saved for the configured gateway; other gateways' keys stay.",
		"Does not revoke a server key or unset inherited environment variables.",
	}},
	{"models", "", []string{"List your models by publisher, with exact ids for harness flags."}},
}

var helpOutput = []string{
	"Help and the model list go to stdout. Prompts, progress, previews, errors and",
	"next steps go to stderr. --plain, NO_COLOR or TERM=dumb use numbered prompts",
	"with no colour or cursor motion. Help needs no credentials or configuration.",
	"--version prints build metadata without reading credentials or configuration.",
	"Native arguments must follow -- and are forwarded literally.",
}

// writeHelp styles only terminal stdout when neither the launcher flag nor the environment requests plain text.
func writeHelp(deps Deps, environ []string, plain bool) {
	u := newUI(deps.Stdout, deps.Term.StdoutTTY, nil, plain || plainRequested(environ))
	u.printf("%s aiven-ai-gateway <command> [args]\n", u.paint(bold, "usage:"))
	u.heading("Commands")
	for _, c := range helpCommands {
		head := "  " + u.paint(bold+";"+cyan, c.name)
		if c.args != "" {
			head += " " + c.args
		}
		u.printf("%s\n", head)
		for _, l := range c.about {
			u.line(6, l)
		}
	}
	u.printf("\n  %s claude | codex | pi | opencode | omp\n", u.paint(bold, "harness:"))
	u.heading("Output")
	for _, l := range helpOutput {
		u.line(2, l)
	}
}
