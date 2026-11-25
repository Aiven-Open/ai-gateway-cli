// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"regexp"
	"strings"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
)

const (
	longName  = "aiven-ai-gateway"
	shortName = "avn-aig"
	// npmBinName is the bin npm picks for npx @aivenio/ai-gateway, the package's unscoped name.
	npmBinName = "ai-gateway"
	npxName    = "npx @aivenio/ai-gateway"
	// InvokedAsVar is set by the npm wrapper, which starts the binary by its absolute path and so hides the name typed.
	InvokedAsVar = "AIVEN_AI_GATEWAY_INVOKED_AS"
)

// commandNames are the subcommands of the launcher.
var commandNames = []string{"launch", "setup", "login", "logout", "models"}

// CommandName is the command that reaches this binary again the way the user ran it. Only the wrapper's four values
// of InvokedAsVar count, so the environment cannot put other text into a printed command.
func CommandName(argv0 string, environ []string) string {
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		if !config.SameVar(name, InvokedAsVar) {
			continue
		}
		switch value {
		case "npx":
			return npxName
		case longName, shortName, npmBinName:
			return value
		}
	}
	base := argv0[strings.LastIndexAny(argv0, `/\`)+1:]
	if strings.TrimSuffix(strings.ToLower(base), ".exe") == shortName {
		return shortName
	}
	return longName
}

func (d Deps) command() string {
	if d.Command == "" {
		return longName
	}
	return d.Command
}

// hintPattern matches a runnable hint: the long name, a space, then a subcommand, --help or a harness.
var hintPattern = regexp.MustCompile(`\b` + longName + ` ((?:` +
	strings.Join(append(append([]string{"--help"}, commandNames...), toolNames...), "|") + `)\b)`)

// rewriteHints puts cmd in place of the long name in every runnable hint of s; other mentions stay.
func rewriteHints(s, cmd string) string {
	if cmd == "" || cmd == longName {
		return s
	}
	return hintPattern.ReplaceAllStringFunc(s, func(m string) string {
		return cmd + strings.TrimPrefix(m, longName)
	})
}

// hintSelector draws inline choices with their hints in the invoked command's form.
type hintSelector struct {
	selector
	cmd string
}

func (h hintSelector) Select(c choice) ([]int, error) {
	c.title, c.subtitle = rewriteHints(c.title, h.cmd), rewriteHints(c.subtitle, h.cmd)
	options := make([]option, len(c.options))
	for i, o := range c.options {
		o.label, o.detail = rewriteHints(o.label, h.cmd), rewriteHints(o.detail, h.cmd)
		options[i] = o
	}
	c.options = options
	return h.selector.Select(c)
}
