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
	prepare func(config.Env, []models.Model, bool, harness.ModelSelection) (harness.Plan, error)
	run     func(config.Env, []models.Model, harness.ModelSelection) string
	bin     string
	moved   []string
}{
	"pi": {
		userConfig(pi.Setup),
		func(_ config.Env, t []models.Model, sel harness.ModelSelection) string {
			return "pi --model " + shellQuoteForOS(pi.ModelFor(t, sel), runtime.GOOS)
		},
		"pi",
		[]string{"PI_CODING_AGENT_DIR"},
	},
	"opencode": {
		userConfig(opencode.Setup),
		func(_ config.Env, t []models.Model, sel harness.ModelSelection) string {
			return "opencode -m " + shellQuoteForOS(opencode.ModelFor(t, sel), runtime.GOOS)
		},
		"opencode",
		[]string{"OPENCODE_CONFIG", "OPENCODE_CONFIG_DIR"},
	},
	"codex": {
		userConfig(codex.Setup),
		func(config.Env, []models.Model, harness.ModelSelection) string { return "codex -p aiven" },
		"codex",
		[]string{"CODEX_HOME"},
	},
	"claude": {
		connectClaude,
		func(env config.Env, _ []models.Model, _ harness.ModelSelection) string {
			return claudeDirectCommand(runtime.GOOS, env.UserHome)
		},
		"claude",
		[]string{"CLAUDE_CONFIG_DIR"},
	},
	"omp": {
		userConfig(omp.Setup),
		func(_ config.Env, t []models.Model, sel harness.ModelSelection) string {
			return "omp --model " + shellQuoteForOS(omp.ModelFor(t, sel), runtime.GOOS)
		},
		"omp",
		[]string{"PI_CODING_AGENT_DIR", "OMP_PROFILE", "PI_PROFILE", "PI_CONFIG_DIR"},
	},
}

// claudeDirectCommand formats Claude's direct --settings invocation for the target OS. On Windows it uses the
// resolved userHome path so both PowerShell and cmd.exe receive the literal file without relying on %USERPROFILE%.
func claudeDirectCommand(goos, userHome string) string {
	if goos == "windows" {
		path := strings.ReplaceAll(claude.SettingsFile, "/", `\`)
		if userHome != "" {
			path = strings.TrimRight(userHome, `\/`) + `\` + path
		}
		return "claude --settings " + shellQuoteForOS(path, "windows")
	}
	return "claude --settings ~/" + claude.SettingsFile
}

// moved refuses redirects that would make setup and the Console's native instructions name different files.
func moved(env config.Env, tool string) error {
	for _, kv := range env.Environ {
		name, v, _ := strings.Cut(kv, "=")
		matches := func(m string) bool { return config.SameVar(name, m) }
		if v != "" && slices.ContainsFunc(setupTools[tool].moved, matches) {
			return fmt.Errorf("%s is set, so %s would not read the config aiven-ai-gateway writes; "+
				"clear %s in your environment and rerun, or configure %s by hand", name, tool, name, tool)
		}
	}
	return nil
}

func userConfig(
	prepare func(harness.Input) (harness.Plan, error),
) func(config.Env, []models.Model, bool, harness.ModelSelection) (harness.Plan, error) {
	return func(
		env config.Env,
		t []models.Model,
		asDefault bool,
		sel harness.ModelSelection,
	) (harness.Plan, error) {
		return prepare(
			harness.Input{
				Table:      t,
				BaseURL:    env.BaseURL,
				UserHome:   env.UserHome,
				ConfigHome: env.ConfigHome,
				Default:    asDefault,
				Selection:  sel,
			},
		)
	}
}

func connectClaude(
	env config.Env,
	t []models.Model,
	asDefault bool,
	sel harness.ModelSelection,
) (harness.Plan, error) {
	return claude.Setup(
		claude.Input{
			Table:     t,
			BaseURL:   env.BaseURL,
			UserHome:  env.UserHome,
			WorkDir:   env.WorkDir,
			Default:   asDefault,
			Selection: sel,
		},
	)
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
			return setupOptions{}, errors.New(
				"do not pass API keys on the command line; use --key-stdin or interactive prompt",
			)
		default:
			return setupOptions{}, errors.New(
				"setup: unexpected argument. Run: aiven-ai-gateway setup --help",
			)
		}
	}
	return o, nil
}

type setupTarget struct {
	out          *targetOutcome
	snapshot     harness.Plan
	plan         harness.Plan
	existing     configuredModel
	badge        string
	preflightErr error
	missing      bool
}

type setupSession struct {
	ctx        context.Context
	env        config.Env
	deps       Deps
	opts       setupOptions
	p          presenter
	run        *setupRun
	gw         gateway.Client
	key        string
	table      []models.Model
	missing    []string
	candidates []*setupTarget
	selected   []*setupTarget
	probeCache map[probeKey]probeResult
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
	if o.tool == "" && deps.Prompter == nil {
		return fail(
			deps,
			"setup: a harness is required without a terminal; use login for credential-only input",
		)
	}
	s := &setupSession{
		ctx:        ctx,
		env:        env,
		deps:       deps,
		opts:       o,
		p:          p,
		run:        &setupRun{env: env, p: p, credential: "not saved by this run; no key was checked"},
		gw:         deps.gateway(env.BaseURL),
		probeCache: map[probeKey]probeResult{},
	}
	return s.execute()
}

func (s *setupSession) execute() int {
	// Step 1: Local discovery first (zero network, 1 ms).
	if s.opts.tool != "" {
		t := s.preflightTool(s.opts.tool)
		s.run.targets = []*targetOutcome{t.out}
		if t.preflightErr != nil {
			if t.missing {
				t.out.status = statusMissing
			} else {
				t.out.status = statusFailed
			}
			t.out.reason = t.preflightErr.Error()
			_ = fail(s.deps, t.out.tool+": "+t.preflightErr.Error())
			return s.run.finish()
		}
		s.candidates = []*setupTarget{t}
		s.selected = []*setupTarget{t}
	} else {
		installed, missing := discoverTools(s.deps)
		s.missing = missing
		if len(installed) == 0 {
			s.p.ui.line(0, "No supported coding harnesses found on PATH.")
			s.p.ui.line(0, "Supported harnesses: "+strings.Join(toolNames, ", ")+".")
			s.p.ui.line(0, "Install one, then run: aiven-ai-gateway setup")
			s.p.ui.line(0, "Run 'aiven-ai-gateway login' to save your key now.")
			return 0
		}
		for _, tool := range installed {
			s.candidates = append(s.candidates, s.preflightTool(tool))
		}
	}

	// Step 2: Opening rail header + obtain & validate key + fetch /v1/models.
	if s.p.ui.color {
		s.p.ui.railStart("Aiven AI Gateway " + s.p.ui.th.g.dot + " " + safe(s.env.BaseURL))
	} else {
		s.p.ui.line(0, "Gateway: "+safe(s.env.BaseURL))
	}
	key, table, err := obtainKey(s.ctx, s.env, s.deps, s.p, s.opts)
	if err != nil {
		return s.run.sharedFailure(s.deps, "reading the key", err)
	}
	s.key = key
	if table == nil {
		if table, err = fetchCatalogue(s.ctx, s.gw, s.key, s.p); err != nil {
			return s.run.sharedFailure(s.deps, "the model access check", err)
		}
	}
	s.table = table
	s.run.table = table

	// Step 3: Populate read-only baselines and live Beat 1 badges for every candidate.
	s.inspectCandidates()

	// Step 4: Reversible 3-Beat + Review loop.
	approved, exitCode, done := s.runBeats()
	if done {
		return exitCode
	}

	// Step 5: Guarded save & write (never save key or prefs when 0 configurations were approved).
	return s.commitApproved(approved)
}

func (s *setupSession) preflightTool(tool string) *setupTarget {
	t := &setupTarget{out: &targetOutcome{tool: tool}}
	err := moved(s.env, tool)
	if err == nil && tool == "pi" && runtime.GOOS == "windows" {
		_, err = pi.WindowsShell(
			s.env.Environ,
			func(path string) bool { _, err := os.Stat(path); return err == nil },
			s.deps.Runner.LookPath,
		)
	}
	if _, lookup := s.deps.Runner.LookPath(tool); err == nil && lookup != nil {
		t.missing = true
		err = fmt.Errorf(
			"%s is required but not installed; install it, then run aiven-ai-gateway setup %s",
			tool,
			tool,
		)
	}
	if err == nil {
		t.snapshot, err = prepareSnapshot(s.env, tool)
	}
	if err != nil {
		t.preflightErr = err
		t.badge = "! config error"
	}
	return t
}

func (s *setupSession) inspectCandidates() {
	for _, t := range s.candidates {
		if t.preflightErr != nil {
			continue
		}
		plan0, err := applySnapshot(
			s.env,
			s.table,
			t.out.tool,
			false,
			t.snapshot,
			harness.ModelSelection{},
		)
		if err != nil {
			t.preflightErr = err
			t.badge = "! config error"
			if slices.Contains(s.selected, t) {
				t.out.status, t.out.reason = statusFailed, err.Error()
				_ = fail(s.deps, t.out.tool+": "+err.Error())
			}
			continue
		}
		t.plan = plan0
		t.existing = inspectConfiguredModel(t.out.tool, s.env, s.table, t.snapshot, plan0)
		t.badge = toolBadge(t.out.tool, s.env, s.table, t.existing)
	}
}

func (s *setupSession) runBeats() (approved []approvedWrite, exitCode int, done bool) {
	const (
		beatHarnesses = iota
		beatModels
		beatDirect
		beatReview
	)
	interactive := !s.opts.yes && s.deps.Prompter != nil
	hasBeat1 := s.opts.tool == ""
	step := beatModels
	if hasBeat1 {
		step = beatHarnesses
	} else if !interactive {
		step = beatReview
	}

	var prevPicked map[string]bool
	for {
		switch step {
		case beatHarnesses:
			picked, err := pickToolsBeat1(s.deps, s.p, s.candidates, s.missing, prevPicked)
			if errors.Is(err, errCancelled) || errors.Is(err, errInputEnded) {
				s.p.ui.line(0, "Setup cancelled; nothing was changed.")
				return nil, 1, true
			}
			if err != nil {
				return nil, fail(s.deps, err.Error()), true
			}
			if len(picked) == 0 {
				s.p.ui.line(0, laterHint)
				return nil, 0, true
			}
			prevPicked = map[string]bool{}
			s.selected = nil
			s.run.targets = nil
			byTool := map[string]*setupTarget{}
			for _, t := range s.candidates {
				byTool[t.out.tool] = t
			}
			for _, name := range picked {
				prevPicked[name] = true
				t := byTool[name]
				if t == nil {
					t = s.preflightTool(name)
				}
				// Reset per-selection state cleanly on re-entry.
				t.out.status = statusPending
				t.out.reason = ""
				if t.preflightErr != nil {
					if t.missing {
						t.out.status = statusMissing
					} else {
						t.out.status = statusFailed
					}
					t.out.reason = t.preflightErr.Error()
					_ = fail(s.deps, t.out.tool+": "+t.preflightErr.Error())
				}
				s.selected = append(s.selected, t)
				s.run.targets = append(s.run.targets, t.out)
			}
			if len(s.eligible()) == 0 {
				return nil, s.run.finish(), true
			}
			if !interactive {
				step = beatReview
			} else {
				step = beatModels
			}

		case beatModels:
			eligible := s.eligible()
			if len(eligible) == 0 {
				return nil, s.run.finish(), true
			}
			code, nextStep, stop := s.runBeatModels(eligible, hasBeat1)
			if stop {
				return nil, code, true
			}
			step = nextStep

		case beatDirect:
			eligible := s.eligible()
			summary, err := selectDirectInvocation(s.deps, s.p, s.env, eligible)
			if errors.Is(err, errBack) {
				step = beatModels
				continue
			}
			if errors.Is(err, errCancelled) || errors.Is(err, errInputEnded) {
				return nil, s.run.cancelAfterKey(s.deps, s.table, err), true
			}
			if err != nil {
				return nil, s.run.cancelAfterKey(s.deps, s.table, err), true
			}
			s.p.ui.railDone("Default provider", summary)
			step = beatReview

		case beatReview:
			// Reset pending targets before re-planning (in case we looped back from Review).
			var active []*setupTarget
			for _, t := range s.selected {
				if t.preflightErr != nil {
					continue
				}
				t.out.status = statusPending
				t.out.reason = ""
				plan, err := applySnapshot(
					s.env,
					s.table,
					t.out.tool,
					t.out.asDefault,
					t.snapshot,
					t.out.selection,
				)
				if err != nil {
					t.out.status, t.out.reason = statusFailed, err.Error()
					continue
				}
				t.plan = plan
				active = append(active, t)
			}
			if blocked(s.env, active, s.table) {
				s.run.credential = "not saved by this run; existing key files unchanged; " +
					"a selected tool's configured model is not granted by this key"
				return nil, s.run.finish(), true
			}
			ready := checkProtocols(s.ctx, s.gw, s.key, active, s.table, s.run, s.probeCache)
			if len(ready) == 0 {
				s.run.credential = "not saved by this run; existing key files unchanged; " +
					"no selected tool passed its basic protocol checks"
				return nil, s.run.finish(), true
			}
			if err := s.ctx.Err(); err != nil {
				return nil, s.run.sharedFailure(s.deps, "the basic protocol checks", err), true
			}
			// Close the vertical rail BEFORE Review table, unified diffs and closing launch commands.
			if s.p.ui.color {
				s.p.ui.railEnd("Review")
			}
			app, outcome := reviewAndApprove(
				s.deps,
				s.p,
				s.env,
				s.opts.yes,
				interactive,
				ready,
				s.table,
			)
			switch outcome {
			case reviewBack:
				step = beatDirect
				continue
			case reviewCancelled:
				s.run.credential = "not saved by this run; existing key files unchanged; setup was cancelled"
				return nil, s.run.finish(), true
			default:
				return app, 0, false
			}
		}
	}
}

func (s *setupSession) eligible() []*setupTarget {
	var out []*setupTarget
	for _, t := range s.selected {
		if t.preflightErr == nil {
			out = append(out, t)
		}
	}
	return out
}

func (s *setupSession) runBeatModels(
	eligible []*setupTarget,
	hasBeat1 bool,
) (exitCode, nextStep int, stop bool) {
	const (
		beatHarnesses = 0
		beatModels    = 1
		beatDirect    = 2
	)
	// Project-level Claude overrides (.claude/settings*.json) are never written by setup, so refuse
	// immediately instead of offering a repair that cannot fix them.
	for _, t := range eligible {
		if t.existing.present && !t.existing.granted && t.existing.unrepairable {
			return s.refuseUngranted(
				eligible,
				notGranted(t.existing.file, t.existing.model, s.table),
			), 0, true
		}
	}
	if len(eligible) >= 2 {
		decision, err := selectMultiToolModels(s.deps, s.p, s.env, eligible, s.table, hasBeat1)
		if errors.Is(err, errBack) && hasBeat1 {
			return 0, beatHarnesses, false
		}
		if errors.Is(err, errNotGranted) {
			return s.refuseUngranted(eligible, err), 0, true
		}
		if err != nil {
			return s.run.cancelAfterKey(s.deps, s.table, err), 0, true
		}
		if !decision.customize {
			for _, t := range eligible {
				if sel, ok := decision.perTool[t.out.tool]; ok {
					t.out.selection = sel
				} else {
					t.out.selection = decision.shared
				}
			}
			s.p.ui.railDone("Models", decision.summaryLabel)
			return 0, beatDirect, false
		}
	}

	// Single tool OR Customize per tool… (supports stepping backward across tools and out to Beat 2 / Beat 1).
	var chosenLabels []string
	i := 0
	for i < len(eligible) {
		t := eligible[i]
		allowBack := hasBeat1 || len(eligible) >= 2 || i > 0
		sel, label, err := selectToolModel(
			s.deps,
			s.p,
			s.env,
			t.out.tool,
			s.table,
			t.existing,
			i+1,
			len(eligible),
			allowBack,
		)
		if errors.Is(err, errBack) {
			if i > 0 {
				i--
				if len(chosenLabels) > i {
					chosenLabels = chosenLabels[:i]
				}
				continue
			}
			if len(eligible) >= 2 {
				return 0, beatModels, false
			}
			if hasBeat1 {
				return 0, beatHarnesses, false
			}
			return s.run.cancelAfterKey(s.deps, s.table, errCancelled), 0, true
		}
		if errors.Is(err, errNotGranted) {
			return s.refuseUngranted(eligible, err), 0, true
		}
		if err != nil {
			return s.run.cancelAfterKey(s.deps, s.table, err), 0, true
		}
		t.out.selection = sel
		if len(chosenLabels) == i {
			chosenLabels = append(chosenLabels, toolLabel(t.out.tool)+": "+label)
		} else {
			chosenLabels[i] = toolLabel(t.out.tool) + ": " + label
		}
		i++
	}
	summary := strings.Join(chosenLabels, ", ")
	if len(eligible) == 1 && len(chosenLabels) == 1 {
		summary = strings.TrimPrefix(chosenLabels[0], toolLabel(eligible[0].out.tool)+": ")
	}
	s.p.ui.railDone("Models", summary)
	return 0, beatDirect, false
}

func (s *setupSession) refuseUngranted(eligible []*setupTarget, err error) int {
	for _, t := range eligible {
		if t.existing.present && !t.existing.granted {
			t.out.status, t.out.reason = statusFailed, err.Error()
		} else if t.out.status == statusPending {
			t.out.reason = "the key was not saved, since another selected tool's model is not granted"
		}
	}
	s.run.credential = "not saved by this run; existing key files unchanged; " +
		"a selected tool's configured model is not granted by this key"
	return s.run.finish()
}

func (s *setupSession) commitApproved(approved []approvedWrite) int {
	// Populate resolvedModel for ready targets (including unchanged ones).
	for _, t := range s.selected {
		if t.preflightErr == nil {
			t.out.resolvedModel = resolvedToolModel(t.out.tool, s.env, s.table, t.plan, t.out.selection)
		}
	}
	if len(approved) == 0 {
		hasUnchanged := false
		for _, o := range s.run.targets {
			if o.status == statusUnchanged {
				hasUnchanged = true
				break
			}
		}
		if hasUnchanged {
			s.run.credential = "unchanged; existing key files unchanged"
		} else {
			s.run.credential = "not saved by this run; existing key files unchanged; " +
				"no configuration changes were approved"
		}
		return s.run.finish()
	}

	s.p.begin("Saving the key")
	where, err := saveKey(s.env, s.deps, s.key)
	if err != nil {
		s.p.failed("the key was not saved")
		for _, item := range approved {
			item.target.out.status = statusPending
			item.target.out.reason = "not attempted: saving the key failed"
		}
		return s.run.sharedFailure(s.deps, "saving the key", err)
	}
	s.p.passed("saved to " + where)
	s.run.credential = "saved to " + where

	prefUpdates := map[string]string{}
	for _, item := range approved {
		writeApproved(s.deps, item)
		if item.target.out.status == statusConfigured && item.prefAlias != "" {
			prefUpdates[item.target.out.tool] = item.prefAlias
		}
	}
	if len(prefUpdates) > 0 {
		if err := config.SaveModelPrefs(
			s.env.UserHome,
			s.env.BaseURL,
			prefUpdates,
			s.deps.writeFile,
		); err != nil {
			_ = fail(s.deps, "saving launcher model preferences: "+err.Error())
			return 1
		}
	}
	return s.run.finish()
}

// obtainKey reads explicit stdin, a working saved key, a confirmed environment key or hidden terminal input.
// A non-nil table means the key already passed model listing.
func obtainKey(
	ctx context.Context,
	env config.Env,
	deps Deps,
	p presenter,
	o setupOptions,
) (string, []models.Model, error) {
	if !p.ui.color {
		p.ui.heading("Key check")
	}
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
		if !p.ui.color {
			p.ui.line(2, "Checking your saved key.")
		}
		table, err := checkCandidate(ctx, env, deps, p, fileKey)
		if err != nil {
			return "", nil, err
		}
		if table != nil {
			if p.ui.color {
				p.ui.railOK(fmt.Sprintf(
					"Key %s %s %d models (%s)",
					maskKey(fileKey),
					p.ui.th.g.dot,
					len(table),
					catalogueFamilies(table),
				))
			} else {
				p.ui.line(
					2,
					fmt.Sprintf(
						"Using saved key %s (run 'aiven-ai-gateway login' to replace).",
						maskKey(fileKey),
					),
				)
			}
			return fileKey, table, nil
		}
	}
	envKey := creds.FromEnv(env.Environ)
	if envKey != "" && envKey != fileKey && deps.Prompter != nil {
		approved, err := confirmEnvKey(deps, p, env, envKey)
		if err != nil {
			return "", nil, err
		}
		if approved {
			table, err := checkCandidate(ctx, env, deps, p, envKey)
			if err != nil {
				return "", nil, err
			}
			if table != nil {
				if p.ui.color {
					p.ui.railOK(fmt.Sprintf(
						"Key %s %s %d models (%s)",
						maskKey(envKey),
						p.ui.th.g.dot,
						len(table),
						catalogueFamilies(table),
					))
				}
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

func confirmEnvKey(deps Deps, p presenter, env config.Env, envKey string) (bool, error) {
	if p.inline != nil {
		c := choice{
			title:    "Use " + creds.EnvVar + " (" + maskKey(envKey) + ")?",
			subtitle: "Gateway: " + safe(env.BaseURL),
			options: []option{
				{label: "Use " + creds.EnvVar, detail: maskKey(envKey) + " with " + safe(env.BaseURL)},
				{label: "Enter a different key", detail: "paste key with hidden input"},
				{label: "Cancel", detail: "exit without reading or saving a key"},
			},
			rail:  true,
			theme: &p.ui.th,
		}
		picked, err := p.inline.Select(c)
		if err != nil {
			return false, err
		}
		if picked[0] == 2 {
			return false, errCancelled
		}
		return picked[0] == 0, nil
	}
	p.ui.line(2, "Key source: "+creds.EnvVar)
	p.ui.line(2, "Key: "+maskKey(envKey))
	p.ui.line(2, "Use this key with "+safe(env.BaseURL)+"?")
	p.ui.line(4, "1) Use "+creds.EnvVar)
	p.ui.line(4, "2) Enter a different key (default)")
	p.ui.line(4, "3) Cancel")
	for range 3 {
		answer, err := deps.Prompter.ReadLine("Choose key option (Enter for 2): ")
		if errors.Is(err, io.EOF) || errors.Is(err, errInputEnded) {
			return false, errInputEnded
		}
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "1", "y", "yes":
			return true, nil
		case "", "2", "n", "no":
			return false, nil
		case "3", "c", "cancel":
			return false, errCancelled
		}
		p.ui.line(2, "Choose a number from 1 to 3.")
	}
	return false, errors.New("no answer: expected key option number")
}

func catalogueFamilies(table []models.Model) string {
	groups := models.Grouped(table)
	var names []string
	for _, g := range groups {
		if g.Name != "" {
			names = append(names, g.Name)
		}
		if len(names) == 3 {
			break
		}
	}
	if len(groups) > 3 {
		names = append(names, fmt.Sprintf("+%d more", len(groups)-3))
	}
	return strings.Join(names, ", ")
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

// fetchCatalogue lists the key's models, using a live spinner on styled terminals and durable announcement in plain mode.
func fetchCatalogue(
	ctx context.Context,
	gw gateway.Client,
	key string,
	p presenter,
) ([]models.Model, error) {
	if p.ui.color {
		var table []models.Model
		err := p.ui.runWithProgress(p.ui.railPrefix(), "Retrieving available models…", func() error {
			var e error
			table, e = gw.Models(ctx, key)
			return e
		})
		if err != nil {
			p.ui.railFail(gatewayCategory(err))
			return nil, err
		}
		return table, nil
	}
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
func checkCandidate(
	ctx context.Context,
	env config.Env,
	deps Deps,
	p presenter,
	key string,
) ([]models.Model, error) {
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
