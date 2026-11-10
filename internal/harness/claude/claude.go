// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package claude prepares a Claude Code launch against the gateway.
package claude

import (
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

// Claude Code assumes 200K for model ids it does not know; the `[1m]` suffix declares 1M and is stripped before the request.
const (
	suffixFloor = 1_000_000
	suffix      = "[1m]"
)

// suffixes matches the `[1m]` suffix as Claude Code strips it: in any case, and repeated.
var suffixes = regexp.MustCompile(`(?i)(\[1m\])+$`)

// bare is model as Claude Code names it, without surrounding space or the `[1m]` suffix.
func bare(model string) string {
	return strings.TrimSpace(suffixes.ReplaceAllString(strings.TrimSpace(model), ""))
}

// Input is everything setup and launch need; the table is non-empty.
type Input struct {
	Table   []models.Model
	BaseURL string
	// UserHome holds SettingsFile, which setup writes.
	UserHome string
	// Default wires the user's own settings.json too.
	Default bool
	// WorkDir is where project `.claude/settings*.json` pins are read from.
	WorkDir string
	// Args are forwarded to claude after `--settings`.
	Args []string
}

// slots are the aliases chosen for each Claude model slot, before the `[1m]` suffix.
type slots struct{ main, opus, sonnet, haiku, fable, custom string }

// wiring computes the suffixed main model id and slot ids for the chosen slots.
func wiring(t []models.Model, s slots) (string, map[string]string) {
	ids := map[string]string{
		"OPUS":   withSuffix(t, s.opus),
		"SONNET": withSuffix(t, s.sonnet),
		"HAIKU":  withSuffix(t, s.haiku),
		"FABLE":  withSuffix(t, s.fable),
		"CUSTOM": withSuffix(t, s.custom),
	}
	return withSuffix(t, s.main), ids
}

const (
	maxContextTokens  = "CLAUDE_CODE_MAX_CONTEXT_TOKENS"
	autoCompactWindow = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"
)

// windowEnv blanks both window variables, which Claude Code reads as unset, so a ceiling inherited from the
// environment or another settings file cannot lower the window; then it declares the active one.
func windowEnv(name, window string) map[string]string {
	env := map[string]string{maxContextTokens: "", autoCompactWindow: ""}
	if name != "" {
		env[name] = window
	}
	return env
}

// recognised matches the shape of the ids Claude Code has built-in metadata for, such as claude-sonnet-5-5 or a dated
// claude-haiku-4-5-20251001, and not gateway aliases such as claude-opus or claude-haiku-5.5. An id of that shape that
// a Claude Code release does not know yet gets the 200K it assumes, which is short of the window but never past it.
var recognised = regexp.MustCompile(`^claude-(?:opus|sonnet|haiku|fable|mythos)(?:-\d+)+$`)

// windowVar is the variable that declares model's window below 1M, and its value; at 1M the `[1m]` suffix declares it,
// and a model missing from the table declares none. Claude Code ignores CLAUDE_CODE_MAX_CONTEXT_TOKENS for an id it
// recognises, whose window CLAUDE_CODE_AUTO_COMPACT_WINDOW lowers instead, to no less than 100K. That variable cannot
// raise the 200K Claude Code assumes for any other id.
func windowVar(t []models.Model, model string) (string, string) {
	m, ok := models.Find(t, model)
	if !ok || m.Context == nil || *m.Context >= suffixFloor {
		return "", ""
	}
	if recognised.MatchString(model) {
		return autoCompactWindow, strconv.Itoa(*m.Context)
	}
	return maxContextTokens, strconv.Itoa(*m.Context)
}

// running is the model a session started with args runs: the last --model before `--`, else model. Claude Code's
// aliases name slot models; opusplan runs Sonnet outside plan mode.
func running(s slots, model string, args []string) string {
	for i := 0; i < len(args) && args[i] != "--"; i++ {
		if v, ok := strings.CutPrefix(args[i], "--model="); ok {
			model = v
		} else if args[i] == "--model" && i+1 < len(args) {
			i++
			model = args[i]
		}
	}
	model = bare(model)
	switch strings.ToLower(model) {
	case "opus":
		return s.opus
	case "sonnet", "opusplan":
		return s.sonnet
	case "haiku":
		return s.haiku
	case "fable":
		return s.fable
	}
	return model
}

// APIKeyHelper is the key helper Claude Code runs through its shell for the gateway at baseURL, which prints that
// gateway's key file. Claude Code caches its output for five minutes and runs it again after a 401 or 403.
func APIKeyHelper(baseURL string) string { return creds.CommandLine(baseURL, runtime.GOOS) }

// SettingsFile is the dedicated setup destination; claude --settings adds it to a session.
const SettingsFile = ".claude/aiven.settings.json"

// Setup merges the gateway wiring into ~/.claude/aiven.settings.json, keeping every other setting and env entry.
// The user's own settings.json is left alone, so only sessions started with --settings use the gateway, unless
// in.Default asks to wire it the same way.
func Setup(in Input) (harness.Plan, error) {
	if in.UserHome == "" {
		return harness.Plan{}, config.ErrNoHome
	}
	if _, err := harness.ReadObject(filepath.Join(in.UserHome, ".claude", "settings.json")); err != nil {
		return harness.Plan{}, err
	}
	if in.WorkDir != "" {
		if _, err := withPins(slots{}, nil, in.UserHome, in.WorkDir); err != nil {
			return harness.Plan{}, err
		}
	}
	var plan harness.Plan
	names := []string{SettingsFile}
	if in.Default {
		names = append(names, filepath.Join(".claude", "settings.json"))
	}
	for _, name := range names {
		f, err := wire(in, filepath.Join(in.UserHome, name))
		if err != nil {
			return harness.Plan{}, err
		}
		plan.Files = append(plan.Files, f)
	}
	return plan, nil
}

func wire(in Input, path string) (harness.File, error) {
	src, cfg, err := harness.ReadJSON(path)
	var env map[string]any
	if err == nil {
		env, err = harness.Object(cfg, "env", path)
	}
	if err != nil {
		return harness.File{}, err
	}
	s := defaultSlots(in.Table)
	main, ids := wiring(in.Table, s)
	cfg["apiKeyHelper"] = APIKeyHelper(in.BaseURL)
	// A model the user chose in this file stays, and so does an ANTHROPIC_MODEL, which Claude Code runs over it. Setup
	// never sets ANTHROPIC_MODEL, since it would outrank the user's choice. A blank one chooses nothing.
	if model, ok := cfg["model"].(string); cfg["model"] == nil || ok && bare(model) == "" {
		cfg["model"] = main
	}
	for k, v := range map[string]string{
		"ANTHROPIC_BASE_URL":             in.BaseURL,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   ids["OPUS"],
		"ANTHROPIC_DEFAULT_SONNET_MODEL": ids["SONNET"],
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  ids["HAIKU"],
		"ANTHROPIC_DEFAULT_FABLE_MODEL":  ids["FABLE"],
		"ANTHROPIC_CUSTOM_MODEL_OPTION":  ids["CUSTOM"],
		"ANTHROPIC_AUTH_TOKEN":           "",
		"ANTHROPIC_API_KEY":              "",
		"CLAUDE_CODE_USE_BEDROCK":        "",
		"CLAUDE_CODE_USE_VERTEX":         "",
		"CLAUDE_CODE_USE_FOUNDRY":        "",
		"AWS_BEARER_TOKEN_BEDROCK":       "",
		"ANTHROPIC_CUSTOM_HEADERS":       "",
	} {
		env[k] = v
	}
	for _, chosen := range []struct {
		in  map[string]any
		key string
	}{{cfg, "model"}, {env, "ANTHROPIC_MODEL"}} {
		if v, ok := chosen.in[chosen.key].(string); ok {
			chosen.in[chosen.key] = current(in.Table, s, v)
		}
	}
	// The window follows the model this file runs.
	effective := pinned(s, cfg)
	name, window := windowVar(in.Table, running(effective, effective.main, nil))
	for k, v := range windowEnv(name, window) {
		env[k] = v
	}
	// Claude Code ignores a settings file holding comments or trailing commas.
	data, err := harness.Edit(path, harness.Strip(src, harness.Strict), cfg)
	if err != nil {
		return harness.File{}, err
	}
	return harness.File{Path: path, Data: data, Backup: true}, nil
}

// current drops a `[1m]` from model when the catalogue window of the model it runs is under 1M: one kept from an
// earlier catalogue would make Claude Code run a model whose window has shrunk as 1M. It never adds one, since model
// may be the user's own choice, and leaves a model the catalogue does not size alone.
func current(t []models.Model, s slots, model string) string {
	m, ok := models.Find(t, running(s, model, nil))
	if bare(model) != strings.TrimSpace(model) && ok && m.Context != nil && *m.Context < suffixFloor {
		return bare(model)
	}
	return model
}

// Selected is the model a session started from the settings file data runs, through Claude Code's aliases: its
// env.ANTHROPIC_MODEL, else its model.
func Selected(data []byte) (string, error) {
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", err
	}
	s := pinned(slots{}, cfg)
	return cmp.Or(running(s, s.main, nil), s.main), nil
}

// defaultSlots prefers production ids, then historical aliases; an empty tier takes the main model.
func defaultSlots(t []models.Model) slots {
	s := slots{
		main:   models.FirstGranted(t, "claude-sonnet-5-5", "deepseek-flash", "glm-fast", "glm", "claude-opus"),
		opus:   models.FirstGranted(t, "claude-opus-5-5", "claude-opus", "claude-fable", "claude-sonnet"),
		sonnet: models.FirstGranted(t, "claude-sonnet-5-5", "glm-fast", "glm", "qwen", "deepseek", "claude-sonnet"),
		// Haiku 5.5 may be listed as `claude-haiku-5-5` or `claude-haiku-5.5`; the dashed id comes first.
		haiku: models.FirstGranted(t, "claude-haiku-5-5", "claude-haiku-5.5", "claude-haiku-4-5", "gemini-flash", "gpt-luna", "claude-haiku", "glm-fast"),
		// A gateway need not serve Fable, so the slot takes Opus first rather than the dearest GPT.
		fable:  models.FirstGranted(t, "claude-opus-5-5", "gpt-astra", "gpt-terra", "claude-fable"),
		custom: models.FirstGranted(t, "gpt-6.1-sol", "gpt-6-sol", "gpt-sol", "gpt-terra", "gpt-luna"),
	}
	if s.custom == "" {
		s.custom = models.FirstNotIn(t, s.opus, s.sonnet, s.haiku, s.fable)
	}
	s.main = cmp.Or(s.main, models.FirstAlias(t))
	s.opus, s.sonnet, s.haiku = cmp.Or(s.opus, s.main), cmp.Or(s.sonnet, s.main), cmp.Or(s.haiku, s.main)
	return s
}

// overlayJSON outranks native settings only for launcher-owned fields; it never holds the key.
func overlayJSON(baseURL, main string, ids map[string]string, name, window string) []byte {
	env := map[string]string{
		"ANTHROPIC_BASE_URL":             baseURL,
		"ANTHROPIC_MODEL":                main,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   ids["OPUS"],
		"ANTHROPIC_DEFAULT_SONNET_MODEL": ids["SONNET"],
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  ids["HAIKU"],
		"ANTHROPIC_DEFAULT_FABLE_MODEL":  ids["FABLE"],
		"ANTHROPIC_CUSTOM_MODEL_OPTION":  ids["CUSTOM"],
		"ANTHROPIC_API_KEY":              "",
		"ANTHROPIC_AUTH_TOKEN":           "",
		"CLAUDE_CODE_USE_BEDROCK":        "",
		"CLAUDE_CODE_USE_VERTEX":         "",
		"CLAUDE_CODE_USE_FOUNDRY":        "",
		"AWS_BEARER_TOKEN_BEDROCK":       "",
		"ANTHROPIC_CUSTOM_HEADERS":       "",
	}
	maps.Copy(env, windowEnv(name, window))
	return harness.JSON(map[string]any{"model": main, "env": env})
}

// slot renders `<prefix>`, `<prefix>_NAME` and `<prefix>_DESCRIPTION`; the last two are blank without an alias.
func slot(t []models.Model, prefix, alias, id string) []string {
	name, desc := "", ""
	if alias != "" {
		m, _ := models.Find(t, alias)
		m.Alias = alias
		name, desc = alias+" (gateway)", m.DisplayName()+" via Aiven AI Gateway"
	}
	return []string{prefix + "=" + id, prefix + "_NAME=" + name, prefix + "_DESCRIPTION=" + desc}
}

func withSuffix(t []models.Model, alias string) string {
	if m, ok := models.Find(t, alias); ok && m.Context != nil && *m.Context >= suffixFloor {
		return alias + suffix
	}
	return alias
}

// Ready accepts actual Aiven wiring in the dedicated or canonical user settings.
func Ready(home, baseURL string) error {
	_, err := configuredSettings(home, baseURL)
	return err
}

func configuredSettings(home, baseURL string) (map[string]any, error) {
	// An empty home would make the paths relative, naming the project's own .claude files.
	if home == "" {
		return nil, config.ErrNoHome
	}
	for _, name := range []string{SettingsFile, ".claude/settings.json"} {
		cfg, err := harness.ReadObject(filepath.Join(home, name))
		if err != nil {
			return nil, err
		}
		env, _ := cfg["env"].(map[string]any)
		if env["ANTHROPIC_BASE_URL"] == baseURL && cfg["apiKeyHelper"] == APIKeyHelper(baseURL) {
			return cfg, nil
		}
	}
	return nil, errors.New("settings have incompatible Aiven gateway or credential helper")
}

// Launch passes only launcher-owned settings inline. Native user/project settings remain additive;
// arbitrary extras in SettingsFile are retained by setup for direct native invocation.
func Launch(in Input) (harness.Plan, error) {
	cfg, err := configuredSettings(in.UserHome, in.BaseURL)
	if err != nil {
		return harness.Plan{}, err
	}
	s, err := withPins(defaultSlots(in.Table), cfg, in.UserHome, in.WorkDir)
	if err != nil {
		return harness.Plan{}, err
	}
	main, ids := wiring(in.Table, s)
	// The window follows the model a native --model runs instead.
	name, window := windowVar(in.Table, running(s, s.main, in.Args))
	child := []string{
		"ANTHROPIC_BASE_URL=" + in.BaseURL, "ANTHROPIC_AUTH_TOKEN=", "ANTHROPIC_API_KEY=",
		"CLAUDE_CODE_USE_BEDROCK=", "CLAUDE_CODE_USE_VERTEX=", "CLAUDE_CODE_USE_FOUNDRY=", "AWS_BEARER_TOKEN_BEDROCK=",
		"ANTHROPIC_CUSTOM_HEADERS=",
		"ANTHROPIC_MODEL=" + main,
	}
	w := windowEnv(name, window)
	child = append(child, maxContextTokens+"="+w[maxContextTokens], autoCompactWindow+"="+w[autoCompactWindow])
	child = append(child, slot(in.Table, "ANTHROPIC_DEFAULT_OPUS_MODEL", s.opus, ids["OPUS"])...)
	child = append(child, slot(in.Table, "ANTHROPIC_DEFAULT_SONNET_MODEL", s.sonnet, ids["SONNET"])...)
	child = append(child, slot(in.Table, "ANTHROPIC_DEFAULT_HAIKU_MODEL", s.haiku, ids["HAIKU"])...)
	child = append(child, slot(in.Table, "ANTHROPIC_DEFAULT_FABLE_MODEL", s.fable, ids["FABLE"])...)
	child = append(child, slot(in.Table, "ANTHROPIC_CUSTOM_MODEL_OPTION", s.custom, ids["CUSTOM"])...)
	var inline map[string]any
	if err := json.Unmarshal(overlayJSON(in.BaseURL, main, ids, name, window), &inline); err != nil {
		return harness.Plan{}, err
	}
	inline["apiKeyHelper"] = APIKeyHelper(in.BaseURL)
	// One line, since cmd.exe cannot pass a newline to a batch file such as npm's claude.cmd.
	settings, err := json.Marshal(inline)
	if err != nil {
		return harness.Plan{}, err
	}
	return harness.Plan{Argv: append([]string{"claude", "--settings", string(settings)}, in.Args...), Env: child}, nil
}
