// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/execx"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/codex"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/omp"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/opencode"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/pi"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

type launchOptions struct {
	tool   string
	native []string
	plain  bool
}

func parseLaunch(args []string) (launchOptions, error) {
	before, native, _ := splitNative(args)
	o := launchOptions{native: native}
	for _, a := range before {
		switch {
		case a == "--plain":
			o.plain = true
		case o.tool == "" && setupTools[a].prepare != nil:
			o.tool = a
		default:
			return launchOptions{}, errors.New("launch: name one harness and put native arguments after --. Run: aiven-ai-gateway launch --help")
		}
	}
	return o, nil
}

func launchCommand(ctx context.Context, args []string, env config.Env, deps Deps) int {
	o, err := parseLaunch(args)
	if err != nil {
		return fail(deps, err.Error())
	}
	tool, native := o.tool, o.native
	p := newPresenter(env, deps, o.plain)
	if tool == "" {
		if deps.Prompter == nil {
			return fail(deps, "launch: a harness is required without a terminal")
		}
		picked, err := pickLaunch(deps, p)
		if errors.Is(err, errCancelled) || err == nil && picked == "" {
			p.ui.line(0, "Nothing launched.")
			return 0
		} else if err != nil {
			return fail(deps, err.Error())
		}
		tool = picked
	}
	if _, err := deps.Runner.LookPath(tool); err != nil {
		return fail(deps, tool+" is required but not installed; install it, then run aiven-ai-gateway setup "+tool)
	}
	if err := moved(env, tool); err != nil {
		return fail(deps, err.Error())
	}
	p.ui.line(0, "Gateway: "+safe(env.BaseURL))
	switch tool {
	case "claude":
		return launchClaude(ctx, native, env, deps, p)
	case "codex":
		return launchCodex(ctx, native, env, deps, p)
	case "pi":
		return launchPi(ctx, native, env, deps, p)
	case "opencode":
		return launchOpencode(ctx, native, env, deps, p)
	case "omp":
		return launchOmp(ctx, native, env, deps, p)
	}
	return fail(deps, "unknown harness")
}

func launchClaude(ctx context.Context, args []string, env config.Env, deps Deps, p presenter) int {
	if err := claude.Ready(env.UserHome, env.BaseURL); err != nil {
		return setupRequired(deps, "claude", err)
	}
	_, table, code := grantedModels(ctx, "claude", env, deps, p)
	if code != 0 {
		return code
	}
	plan, err := claude.Launch(claude.Input{Table: table, BaseURL: env.BaseURL, UserHome: env.UserHome, WorkDir: env.WorkDir, Args: args})
	if err != nil {
		return setupRequired(deps, "claude", err)
	}
	return launch(plan, env, deps, p)
}

func launchCodex(ctx context.Context, args []string, env config.Env, deps Deps, p presenter) int {
	return launchTable(ctx, "codex", args, env, deps, p, codex.Ready, codex.Launch)
}

func launchPi(ctx context.Context, args []string, env config.Env, deps Deps, p presenter) int {
	if env.UserHome != "" {
		if err := pi.SavedCredential(env.UserHome); err != nil {
			return fail(deps, err.Error())
		}
	}
	return launchTable(ctx, "pi", args, env, deps, p, pi.Ready, pi.Launch)
}

func launchOpencode(ctx context.Context, args []string, env config.Env, deps Deps, p presenter) int {
	ready := func(home, baseURL string) (bool, error) { return opencode.Ready(home, env.ConfigHome, baseURL) }
	return launchTable(ctx, "opencode", args, env, deps, p, ready, opencode.Launch)
}

func launchOmp(ctx context.Context, args []string, env config.Env, deps Deps, p presenter) int {
	return launchTable(ctx, "omp", args, env, deps, p, omp.Ready, omp.Launch)
}

// launchTable launches a harness whose launch needs only the key, the table and pass-through args.
func launchTable(ctx context.Context, bin string, args []string, env config.Env, deps Deps, p presenter, ready func(string, string) (bool, error), prepare func(harness.Input) (harness.Plan, error)) int {
	needsEnv, err := ready(env.UserHome, env.BaseURL)
	if err != nil {
		return setupRequired(deps, bin, err)
	}
	if bin == "opencode" {
		if _, err := os.Stat(creds.Path(env.UserHome, env.BaseURL)); err != nil {
			return fail(deps, "OpenCode requires a key saved for this gateway. Run: aiven-ai-gateway login")
		}
	}
	key, table, code := grantedModels(ctx, bin, env, deps, p)
	if code != 0 {
		return code
	}
	plan, err := prepare(harness.Input{Table: table, BaseURL: env.BaseURL, UserHome: env.UserHome, ConfigHome: env.ConfigHome, Args: args})
	if err != nil {
		return setupRequired(deps, bin, err)
	}
	if needsEnv {
		plan.Env = append(plan.Env, creds.EnvVar+"="+key)
	}
	return launch(plan, env, deps, p)
}

func setupRequired(deps Deps, tool string, err error) int {
	return fail(deps, tool+" setup is missing or incompatible: "+err.Error()+". Run: aiven-ai-gateway setup "+tool)
}

// grantedModels checks harnessBin (unless empty) is installed, then reads the stored key and its model table; a nonzero code means the failure is already reported.
func grantedModels(ctx context.Context, harnessBin string, env config.Env, deps Deps, p presenter) (string, []models.Model, int) {
	if harnessBin != "" {
		if _, err := deps.Runner.LookPath(harnessBin); err != nil {
			return "", nil, fail(deps, err.Error())
		}
	}
	key, err := storedKey(env)
	if err != nil {
		return "", nil, fail(deps, err.Error())
	} else if key == "" {
		return "", nil, fail(deps, "no key stored. Run: aiven-ai-gateway login")
	}
	table, err := fetchCatalogue(ctx, deps.gateway(env.BaseURL), key, p)
	if err != nil {
		return "", nil, failGateway(deps, err)
	}
	return key, table, 0
}

// launch accepts only a session plan, making persistent writes an error at the execution boundary. No inference
// request precedes the exec: the session's own first request is the test.
func launch(plan harness.Plan, env config.Env, deps Deps, p presenter) int {
	if len(plan.Files) != 0 {
		return fail(deps, "launch preparation attempted a persistent configuration write")
	}
	childEnv := make([]string, 0, len(env.Environ))
	for _, kv := range env.Environ {
		name, _, _ := strings.Cut(kv, "=")
		if !config.SameVar(name, creds.EnvVar) {
			childEnv = append(childEnv, kv)
		}
	}
	p.ui.line(2, "Starting "+toolLabel(plan.Argv[0])+"; the session now owns this terminal.")
	if err := deps.Runner.Exec(plan.Argv[0], plan.Argv, execx.Merge(childEnv, plan.Env)); err != nil {
		return fail(deps, err.Error())
	}
	return 0
}

// backupNames picks each file's backup name once per run, so the previews, the write and the report all name the copy
// that is made, even if another run takes the next name meanwhile.
type backupNames struct {
	now    time.Time
	picked map[string]string
}

func (b backupNames) name(path string) string {
	if b.picked[path] == "" {
		b.picked[path] = fsutil.BackupPath(path, b.now)
	}
	return b.picked[path]
}
