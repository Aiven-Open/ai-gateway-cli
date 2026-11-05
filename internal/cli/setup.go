// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/gateway"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/codex"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/omp"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/opencode"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/pi"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

// setupTools describe additive persistent wiring and its direct native invocation, which quotes every token taken
// from the catalogue: the printed command is meant to be pasted into a shell.
// A tool with a bin can also be made to use the gateway by default, after which bin alone starts it on Aiven.
// moved lists variables that redirect the native configuration away from the supported destinations.
var setupTools = map[string]struct {
	prepare func(env config.Env, t []models.Model, asDefault bool) (harness.Plan, error)
	run     func([]models.Model) string
	bin     string
	moved   []string
}{
	"pi":       {userConfig(pi.Setup), func(t []models.Model) string { return "pi --model " + shellQuote(pi.Model(t)) }, "pi", []string{"PI_CODING_AGENT_DIR"}},
	"opencode": {userConfig(opencode.Setup), func(t []models.Model) string { return "opencode -m " + shellQuote(opencode.Model(t)) }, "opencode", []string{"OPENCODE_CONFIG", "OPENCODE_CONFIG_DIR"}},
	"codex":    {userConfig(codex.Setup), func([]models.Model) string { return "codex -p aiven" }, "codex", []string{"CODEX_HOME"}},
	"claude":   {connectClaude, func([]models.Model) string { return "claude --settings ~/" + claude.SettingsFile }, "claude", []string{"CLAUDE_CONFIG_DIR"}},
	"omp":      {userConfig(omp.Setup), func(t []models.Model) string { return "omp --model " + shellQuote(omp.Model(t)) }, "omp", []string{"PI_CODING_AGENT_DIR", "OMP_PROFILE", "PI_PROFILE", "PI_CONFIG_DIR"}},
}

// moved refuses redirects that would make setup and the Console's native instructions name different files.
func moved(env config.Env, tool string) error {
	for _, kv := range env.Environ {
		if name, v, _ := strings.Cut(kv, "="); v != "" && slices.ContainsFunc(setupTools[tool].moved, func(m string) bool {
			return config.SameVar(name, m)
		}) {
			return fmt.Errorf("%s is set, so %s would not read the config aiven-ai-gateway writes; unset it and rerun, or configure %s by hand", name, tool, tool)
		}
	}
	return nil
}

func userConfig(prepare func(harness.Input) (harness.Plan, error)) func(config.Env, []models.Model, bool) (harness.Plan, error) {
	return func(env config.Env, t []models.Model, asDefault bool) (harness.Plan, error) {
		return prepare(harness.Input{Table: t, BaseURL: env.BaseURL, UserHome: env.UserHome, ConfigHome: env.ConfigHome, Default: asDefault})
	}
}

func connectClaude(env config.Env, t []models.Model, asDefault bool) (harness.Plan, error) {
	return claude.Setup(claude.Input{Table: t, BaseURL: env.BaseURL, UserHome: env.UserHome, WorkDir: env.WorkDir, Default: asDefault})
}

const connectPrompt = "Paste your Aiven AI Gateway API key (hidden): "

type setupOptions struct {
	tool                  string
	fromStdin, yes, plain bool
}

// parseSetup rejects unknown inputs without echoing a possible pasted credential.
func parseSetup(args []string) (setupOptions, error) {
	var o setupOptions
	for _, a := range args {
		switch name, _, _ := strings.Cut(a, "="); {
		case a == "--key-stdin":
			o.fromStdin = true
		case a == "--yes" || a == "-y":
			o.yes = true
		case a == "--plain":
			o.plain = true
		case setupTools[a].prepare != nil && o.tool == "":
			o.tool = a
		case name == "--key" || name == "--api-key":
			return setupOptions{}, errors.New("do not pass API keys on the command line; use --key-stdin or interactive prompt")
		default:
			return setupOptions{}, errors.New("setup: unexpected argument. Run: aiven-ai-gateway setup --help")
		}
	}
	return o, nil
}

func setup(ctx context.Context, args []string, env config.Env, deps Deps) int {
	o, err := parseSetup(args)
	if err != nil {
		return fail(deps, err.Error())
	}
	if env.UserHome == "" {
		return fail(deps, config.ErrNoHome.Error())
	}
	p := newPresenter(env, deps, o.plain)
	picked := []string{o.tool}
	if o.tool == "" {
		if deps.Prompter == nil {
			return fail(deps, "setup: a harness is required without a terminal; use login for credential-only input")
		}
		picked, err = pickTools(deps, p)
		if errors.Is(err, errCancelled) {
			p.ui.line(0, "Setup cancelled; nothing was changed.")
			return 1
		} else if err != nil {
			return fail(deps, err.Error())
		}
		if len(picked) == 0 {
			p.ui.line(0, laterHint)
			return 0
		}
	}
	run := &setupRun{env: env, p: p, credential: "not saved by this run; no key was checked"}
	// Establish the complete write set, including the optional default, before reading or saving a credential.
	var valid []*targetOutcome
	snapshots := map[string]harness.Plan{}
	for _, target := range picked {
		out := run.add(target)
		err := moved(env, target)
		if err == nil && target == "pi" && runtime.GOOS == "windows" {
			_, err = pi.WindowsShell(env.Environ, func(path string) bool { _, err := os.Stat(path); return err == nil }, deps.Runner.LookPath)
		}
		if _, lookup := deps.Runner.LookPath(target); err == nil && lookup != nil {
			out.status = statusMissing
			err = fmt.Errorf("%s is required but not installed; install it, then run aiven-ai-gateway setup %s", target, target)
		}
		if err == nil && setupTools[target].bin != "" && !o.yes && deps.Prompter != nil {
			out.asDefault, err = confirm(deps.Prompter, fmt.Sprintf(
				"Make Aiven the native default for %s? Enter keeps your current default. [y/N] ", toolLabel(target)), false)
			// An unanswered question stops the whole run: later targets must not save a key on its behalf.
			if errors.Is(err, errInputEnded) {
				return run.cancel(deps, err)
			}
		}
		var snapshot harness.Plan
		if err == nil {
			snapshot, err = prepareSnapshot(env, nil, target, out.asDefault)
		}
		if err != nil {
			if out.status == statusPending {
				out.status = statusFailed
			}
			out.reason = err.Error()
			_ = fail(deps, target+": "+err.Error())
			continue
		}
		snapshots[target] = snapshot
		valid = append(valid, out)
	}
	if len(valid) == 0 {
		return run.finish()
	}
	p.ui.line(0, "Gateway: "+safe(env.BaseURL))
	key, table, err := obtainKey(ctx, env, deps, p, o)
	if err != nil {
		return run.sharedFailure(deps, "reading the key", err)
	}
	gw := deps.gateway(env.BaseURL)
	// A kept saved key passed model listing; inference depends on the selected plans.
	checked := table != nil
	if !checked {
		if table, err = fetchCatalogue(ctx, gw, key, p); err != nil {
			return run.sharedFailure(deps, "the model access check", err)
		}
	}
	plans := map[string]harness.Plan{}
	for _, out := range valid {
		plan, err := applySnapshot(env, table, out.tool, out.asDefault, snapshots[out.tool])
		if err != nil {
			out.status, out.reason = statusFailed, err.Error()
			continue
		}
		plans[out.tool] = plan
	}
	if blocked(valid, plans, table) {
		run.table = table
		run.credential = "not saved by this run; existing key files unchanged; a selected tool's configured model is not granted by this key"
		return run.finish()
	}
	valid = checkProtocols(ctx, gw, key, valid, plans, table, run)
	run.table = table
	if len(valid) == 0 {
		run.credential = "not saved by this run; existing key files unchanged; no selected tool passed its basic protocol checks"
		return run.finish()
	}
	if err := ctx.Err(); err != nil {
		return run.sharedFailure(deps, "the basic protocol checks", err)
	}
	p.begin("Saving the key")
	where, err := saveKey(env, deps, key)
	if err != nil {
		p.failed("the key was not saved")
		return run.sharedFailure(deps, "saving the key", err)
	}
	p.passed("saved to " + where)
	run.credential, run.table = "saved to "+where, table
	for i, out := range valid {
		configureTool(deps, p, o.yes, out, plans[out.tool].Files, table)
		if out.status == statusCancelled {
			for _, rest := range valid[i+1:] {
				rest.status, rest.reason = statusPending, "not attempted after the cancellation"
			}
			break
		}
	}
	return run.finish()
}

// obtainKey reads explicit stdin, a working saved key, a confirmed environment key or hidden terminal input.
// A non-nil table means the key already passed model listing.
func obtainKey(ctx context.Context, env config.Env, deps Deps, p presenter, o setupOptions) (string, []models.Model, error) {
	p.ui.heading("Key check")
	p.ui.line(2, "Setup lists your models, then sends one minimal request per configured protocol")
	p.ui.line(2, "and model, limited to one output token each. These requests may incur usage.")
	p.ui.line(2, "A basic check does not show that the tool's own requests are accepted.")
	if o.fromStdin {
		stdin := deps
		stdin.Prompter = nil
		key, err := readKey(stdin, "")
		return key, nil, err
	}
	fileKey, err := storedKey(env)
	if err != nil {
		return "", nil, err
	}
	if fileKey != "" {
		p.ui.line(2, "Checking your saved key.")
		table, err := checkCandidate(ctx, env, deps, p, fileKey)
		if err != nil {
			return "", nil, err
		}
		if table != nil {
			return keepSavedKey(deps, p, o.yes, fileKey, table)
		}
	}
	envKey := creds.FromEnv(env.Environ)
	if envKey != "" && envKey != fileKey && deps.Prompter != nil {
		p.ui.line(2, "Key source: "+creds.EnvVar)
		p.ui.line(2, "Key: "+maskKey(envKey))
		approved, err := confirm(deps.Prompter, "Use this key with "+safe(env.BaseURL)+"? [y/N] ", false)
		if err != nil {
			return "", nil, err
		}
		if approved {
			table, err := checkCandidate(ctx, env, deps, p, envKey)
			if err != nil {
				return "", nil, err
			}
			if table != nil {
				return envKey, table, nil
			}
		}
	}
	if deps.Prompter == nil {
		return "", nil, errors.New("no usable saved key; use --key-stdin to supply one, or run login")
	}
	key, err := readKey(deps, connectPrompt)
	return key, nil, err
}

// keepSavedKey asks on a terminal whether to keep a working saved key. Without a terminal, or with --yes, it keeps the
// key, since stdin then holds no replacement.
func keepSavedKey(deps Deps, p presenter, yes bool, key string, table []models.Model) (string, []models.Model, error) {
	if deps.Prompter == nil || yes {
		p.ui.line(2, "Using your existing gateway key.")
		return key, table, nil
	}
	keep, err := confirm(deps.Prompter, fmt.Sprintf("Keep the saved key (%s)? [Y/n] ", maskKey(key)), true)
	if err != nil || keep {
		return key, table, err
	}
	p.ui.line(2, "The replacement is checked again before it is saved.")
	key, err = readKey(deps, connectPrompt)
	return key, nil, err
}

// confirm asks a yes-or-no question, where Enter means def, asking again on an unrecognised answer at most three
// times. End of input is errInputEnded, never def.
func confirm(p Prompter, prompt string, def bool) (bool, error) {
	for range 3 {
		answer, err := p.ReadLine(prompt)
		if errors.Is(err, io.EOF) || errors.Is(err, errInputEnded) {
			return false, errInputEnded
		} else if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
	}
	return false, errors.New("no answer: expected y or n")
}

// maskKey shows a known key-type prefix, and the last 4 characters only when at least 12 others stay hidden.
func maskKey(key string) string {
	prefix := ""
	for _, p := range []string{"AVNS_", "sk-"} {
		if strings.HasPrefix(key, p) {
			prefix = p
			break
		}
	}
	if len(key)-len(prefix)-4 < 12 {
		return prefix + "…"
	}
	return prefix + "…" + key[len(key)-4:]
}

// fetchCatalogue lists the key's models, announcing the request before it is sent.
func fetchCatalogue(ctx context.Context, gw gateway.Client, key string, p presenter) ([]models.Model, error) {
	p.begin("Retrieving available models")
	table, err := gw.Models(ctx, key)
	if err != nil {
		p.failed(gatewayCategory(err))
		return nil, err
	}
	p.passed(fmt.Sprintf("%d models available", len(table)))
	return table, nil
}

// rejected classifies authentication only; schema/model refusals are request failures.
func rejected(err error) bool {
	return errors.Is(err, gateway.ErrUnauthorized)
}

// checkCandidate lets setup replace a rejected or no-model key, but never switches on other request failures.
func checkCandidate(ctx context.Context, env config.Env, deps Deps, p presenter, key string) ([]models.Model, error) {
	table, err := fetchCatalogue(ctx, deps.gateway(env.BaseURL), key, p)
	if err == nil {
		return table, nil
	}
	if errors.Is(err, gateway.ErrNoModels) {
		p.ui.line(2, "That key grants no models; it is not used.")
	} else if rejected(err) {
		p.ui.line(2, "The gateway refused that key; it is not used.")
	} else {
		return nil, err
	}
	return nil, nil
}

// gatewayCategory names a failed request's cause without its details, which the error line that follows gives.
func gatewayCategory(err error) string {
	var unreachable *gateway.UnreachableError
	var he *gateway.HTTPError
	switch {
	case errors.Is(err, gateway.ErrUnauthorized):
		return "the gateway rejected the key"
	case errors.Is(err, gateway.ErrNoModels):
		return "the key grants no models"
	case errors.As(err, &unreachable):
		return "the gateway could not be reached"
	case errors.As(err, &he) && he.Status == 429:
		return "the gateway is rate limiting requests"
	case errors.As(err, &he) && he.Status >= 500:
		return "the gateway reported a server error"
	case errors.As(err, &he):
		return fmt.Sprintf("the gateway refused the request (HTTP %d)", he.Status)
	case errors.Is(err, gateway.ErrUnexpected):
		return "the gateway sent an unexpected response"
	}
	return "the request failed"
}
