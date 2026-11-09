// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package cli dispatches aiven-llm commands.
package cli

import (
	"context"
	"fmt"

	"github.com/Aiven-Open/ai-gateway-cli/internal/config"
)

// Run executes one invocation and returns the process exit code.
func Run(ctx context.Context, args []string, env config.Env, deps Deps) int {
	if len(args) == 0 {
		fmt.Fprint(deps.Stdout, usageText)
		return 1
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(deps.Stdout, usageText)
		return 0
	case "login":
		return login(ctx, env, deps)
	case "models":
		return listModels(ctx, env, deps)
	case "logout":
		return logout(env, deps)
	case "claude":
		return launchClaude(ctx, args[1:], env, deps)
	case "omp":
		return launchOmp(ctx, args[1:], env, deps)
	case "codex":
		return launchCodex(ctx, args[1:], env, deps)
	case "pi":
		return launchPi(ctx, args[1:], env, deps)
	case "opencode":
		return launchOpencode(ctx, args[1:], env, deps)
	default:
		fmt.Fprintf(deps.Stderr, "aiven-llm: unknown command: %s\n", args[0])
		return 1
	}
}

const usageText = `usage: aiven-llm <command> [args]
  login              store a gateway key (paste on stdin)
  models             list models you can use, with prices
  logout             remove the stored key
  claude [--isolated] [args]   launch Claude Code with your own profile plus gateway wiring
                     --isolated uses a blank profile under ~/.config/aiven-llm/claude instead
  omp [args]         launch OMP as a role router in the aiven-llm profile: read, grep, glob,
                     task and todo only, so every edit goes through a role agent
  omp init [flags]   pick a gateway alias per role for this project and write .omp/config.yml
                     flags: --architect --builder --cross-reviewer --bulk-reader
                     --boilerplate-writer --advisor off|main|builder|both --advisor-model
  codex|pi|opencode [args]          launch that harness against the gateway
`
