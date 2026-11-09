// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/Aiven-Open/ai-gateway-cli/internal/config"
	"github.com/Aiven-Open/ai-gateway-cli/internal/execx"
	"github.com/Aiven-Open/ai-gateway-cli/internal/fsutil"
	"github.com/Aiven-Open/ai-gateway-cli/internal/harness"
	"github.com/Aiven-Open/ai-gateway-cli/internal/harness/claude"
	"github.com/Aiven-Open/ai-gateway-cli/internal/harness/codex"
	"github.com/Aiven-Open/ai-gateway-cli/internal/harness/omp"
	"github.com/Aiven-Open/ai-gateway-cli/internal/harness/opencode"
	"github.com/Aiven-Open/ai-gateway-cli/internal/harness/pi"
	"github.com/Aiven-Open/ai-gateway-cli/internal/keystore"
	"github.com/Aiven-Open/ai-gateway-cli/internal/models"
)

const isolatedFlag = "--isolated"

func launchClaude(ctx context.Context, args []string, env config.Env, deps Deps) int {
	key, table, code := grantedModels(ctx, "claude", env, deps)
	if code != 0 {
		return code
	}
	in := claude.Input{Table: table, Key: key, BaseURL: env.BaseURL, Home: generatedHome(env), WorkDir: env.WorkDir}
	in.Isolated, in.Args = splitIsolated(args)
	return launch(claude.Prepare(in), env, deps)
}

func launchCodex(ctx context.Context, args []string, env config.Env, deps Deps) int {
	return launchTable(ctx, "codex", args, true, env, deps, func(in harness.Input) (harness.Plan, error) {
		in.ProfileDir = env.CodexHome
		return codex.Prepare(in)
	})
}

func launchPi(ctx context.Context, args []string, env config.Env, deps Deps) int {
	return launchTable(ctx, "pi", args, true, env, deps, func(in harness.Input) (harness.Plan, error) {
		in.ProfileDir = env.PiAgentDir
		return pi.Prepare(in)
	})
}

func launchOpencode(ctx context.Context, args []string, env config.Env, deps Deps) int {
	return launchTable(ctx, "opencode", args, false, env, deps, func(in harness.Input) (harness.Plan, error) { return opencode.Prepare(in), nil })
}

// splitIsolated reports whether args lead with --isolated, and returns the rest.
func splitIsolated(args []string) (bool, []string) {
	if len(args) > 0 && args[0] == isolatedFlag {
		return true, args[1:]
	}
	return false, args
}

// launchOmp dispatches `omp init`; otherwise it checks the plugin next to the binary, before the gateway call or any write: without role agents the router cannot route.
func launchOmp(ctx context.Context, args []string, env config.Env, deps Deps) int {
	if len(args) > 0 && args[0] == "init" {
		return ompInit(ctx, args[1:], env, deps)
	}
	if _, err := deps.Runner.LookPath("omp"); err != nil {
		return fail(deps, err.Error())
	}
	profile := omp.Profile(env.GatewayScope())
	if !omp.PluginInstalled(env.OMPHome, env.XDGDataHome, profile) {
		fmt.Fprint(deps.Stderr, "aiven-llm: "+omp.PluginHint(profile))
		return 1
	}
	key, table, code := grantedModels(ctx, "", env, deps)
	if code != 0 {
		return code
	}
	return launch(omp.Prepare(omp.Input{Table: table, Key: key, BaseURL: env.BaseURL, Home: generatedHome(env), OMPHome: env.OMPHome, Profile: profile, Args: args}), env, deps)
}

// launchTable launches a harness whose launch needs only the key, the table and pass-through args; isolatable harnesses take a leading --isolated.
func launchTable(ctx context.Context, bin string, args []string, isolatable bool, env config.Env, deps Deps, prepare func(harness.Input) (harness.Plan, error)) int {
	key, table, code := grantedModels(ctx, bin, env, deps)
	if code != 0 {
		return code
	}
	in := harness.Input{Table: table, Key: key, BaseURL: env.BaseURL, Home: generatedHome(env), Args: args}
	if isolatable {
		in.Isolated, in.Args = splitIsolated(args)
	}
	plan, err := prepare(in)
	if err != nil {
		return fail(deps, err.Error())
	}
	return launch(plan, env, deps)
}

// generatedHome holds a launch's harness config, one per gateway: a running Claude Code rereads its settings, so another gateway's launch must not rewrite them.
func generatedHome(env config.Env) string {
	if scope := env.GatewayScope(); scope != "" {
		return filepath.Join(env.Home, "gateways", scope)
	}
	return env.Home
}

// grantedModels checks harnessBin (unless empty) is installed, then reads the stored key and its model table; a nonzero code means the failure is already reported.
func grantedModels(ctx context.Context, harnessBin string, env config.Env, deps Deps) (string, []models.Model, int) {
	if harnessBin != "" {
		if _, err := deps.Runner.LookPath(harnessBin); err != nil {
			return "", nil, fail(deps, err.Error())
		}
	}
	rec, err := deps.Keystore.Get(env.BaseURL)
	if errors.Is(err, keystore.ErrNotFound) {
		return "", nil, fail(deps, "no key stored. Run: aiven-llm login")
	}
	if err != nil {
		return "", nil, fail(deps, err.Error())
	}
	table, err := deps.gateway(env.BaseURL).Models(ctx, rec.Key)
	if err != nil {
		return "", nil, failGateway(deps, err)
	}
	return rec.Key, table, 0
}

// launch writes the plan's files, then execs the harness; it returns only when the exec failed.
func launch(plan harness.Plan, env config.Env, deps Deps) int {
	for _, f := range plan.Files {
		if err := fsutil.WriteAtomic(f.Path, f.Data, 0o600); err != nil {
			return fail(deps, err.Error())
		}
	}
	fmt.Fprint(deps.Stderr, plan.Notice)
	if err := deps.Runner.Exec(plan.Argv[0], plan.Argv, execx.Merge(env.Environ, plan.Env)); err != nil {
		return fail(deps, err.Error())
	}
	return 0
}
