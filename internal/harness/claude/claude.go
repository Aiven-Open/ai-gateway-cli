// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package claude prepares a Claude Code launch against the gateway.
package claude

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
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
	// Selection overrides the model or preset chosen during setup; zero value preserves automatic defaults.
	Selection harness.ModelSelection
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
	if id, ok := tierSlot(s, model); ok {
		return id
	}
	return model
}

// tierSlot is the slot model a Claude Code tier alias names, and whether model is such an alias.
func tierSlot(s slots, model string) (string, bool) {
	switch strings.ToLower(model) {
	case "opus":
		return s.opus, true
	case "sonnet", "opusplan":
		return s.sonnet, true
	case "haiku":
		return s.haiku, true
	case "fable":
		return s.fable, true
	}
	return "", false
}

// isCodeAlias reports whether model is one of Claude Code's own model aliases: the four tiers, opusplan and default.
func isCodeAlias(model string) bool {
	_, tier := tierSlot(slots{}, bare(model))
	return tier || strings.EqualFold(bare(model), "default")
}

// APIKeyHelper is the key helper Claude Code runs through its shell for the gateway at baseURL, which prints that
// gateway's key file. Claude Code caches its output for five minutes and runs it again after a 401 or 403.
func APIKeyHelper(baseURL string) string { return creds.CommandLine(baseURL, runtime.GOOS) }

// SettingsFile is the dedicated setup destination; claude --settings adds it to a session.
const SettingsFile = ".claude/aiven.settings.json"

// discoveryVar makes Claude Code list the gateway's Claude models in /model, including one a pick moved out of its
// tier. Observed in Claude Code, not documented.
const discoveryVar = "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"

// discoveryOn reports whether settings cfg turn gateway model discovery on.
func discoveryOn(cfg map[string]any) bool {
	env, _ := cfg["env"].(map[string]any)
	return env[discoveryVar] == "1"
}

// Setup merges the gateway wiring into ~/.claude/aiven.settings.json, keeping every other setting and env entry.
// The user's own settings.json is left alone, so only sessions started with --settings use the gateway, unless
// in.Default asks to wire it the same way.
func Setup(in Input) (harness.Plan, error) {
	if in.UserHome == "" {
		return harness.Plan{}, config.ErrNoHome
	}
	settings := filepath.Join(in.UserHome, ".claude", "settings.json")
	if _, err := harness.ReadObject(settings); err != nil {
		return harness.Plan{}, err
	}
	if in.WorkDir != "" {
		if _, err := withPins(nil, slots{}, nil, in.UserHome, in.WorkDir); err != nil {
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

// dedicatedLayer is the settings of the dedicated file when path is the native one wired beside it on Keep current
// (or --yes), else nil. Only the native path is wired beside the dedicated file, and only with the native default.
func dedicatedLayer(in Input, path string) map[string]any {
	own := filepath.Join(in.UserHome, SettingsFile)
	if !in.Selection.IsZero() || path == own {
		return nil
	}
	// Setup wires the dedicated file first and fails on one that is unreadable, so an error here cannot occur.
	cfg, _ := harness.ReadObject(own)
	return cfg
}

// siblingSlots is env with its tier pins and custom option replaced, as a set, by those of the dedicated file's
// settings: the native file's own pins do not win, and nothing else crosses.
func siblingSlots(sibling, env map[string]any) map[string]any {
	merged := maps.Clone(env)
	siblingEnv, _ := sibling["env"].(map[string]any)
	for _, k := range append([]string{"ANTHROPIC_CUSTOM_MODEL_OPTION"}, tierVars...) {
		delete(merged, k)
		if v, ok := siblingEnv[k]; ok {
			merged[k] = v
		}
	}
	return merged
}

// siblingModel is the model the native file runs on Keep current: its own when the key grants it or it is a tier
// alias, which resolves through the slots, else the dedicated file's. It reports the model to wire, and drops any
// other from cfg so that the fresh path sets it.
func siblingModel(t []models.Model, sibling, cfg map[string]any, s slots) slots {
	if own, isString := cfg["model"].(string); isString && !isCodeAlias(own) &&
		grantedPin(t, cfg, "model", "") == "" {
		delete(cfg, "model")
	}
	s.main = cmp.Or(grantedPin(t, sibling, "model", ""), s.main)
	return s
}

// keepsBlankCustomOption is whether env holds a blank custom option that stays, as it suppresses one inherited from
// the user's settings, on Keep current.
func keepsBlankCustomOption(in Input, env map[string]any) bool {
	v, ok := env["ANTHROPIC_CUSTOM_MODEL_OPTION"].(string)
	return ok && v == "" && in.Selection.IsZero()
}

// tierSource is the env the slots resolve from: the dedicated file's slots under the native one, and for a chosen
// single model the user's own env beneath the file's.
func tierSource(in Input, sibling, env, userSettings map[string]any) map[string]any {
	tierEnv := env
	if sibling != nil {
		tierEnv = siblingSlots(sibling, env)
	}
	if in.Selection.Model != "" && !in.Default && userSettings != nil {
		if userEnv, ok := userSettings["env"].(map[string]any); ok && len(userEnv) > 0 {
			merged := make(map[string]any, len(userEnv)+len(env))
			maps.Copy(merged, userEnv)
			maps.Copy(merged, env)
			tierEnv = merged
		}
	}
	return tierEnv
}

// gatewayEnv is the env entries every wired file sets: the gateway URL, the tier slots and the blanked credentials.
func gatewayEnv(baseURL string, ids map[string]string) map[string]string {
	return map[string]string{
		"ANTHROPIC_BASE_URL":             baseURL,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   ids["OPUS"],
		"ANTHROPIC_DEFAULT_SONNET_MODEL": ids["SONNET"],
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  ids["HAIKU"],
		"ANTHROPIC_DEFAULT_FABLE_MODEL":  ids["FABLE"],
		"ANTHROPIC_AUTH_TOKEN":           "",
		"ANTHROPIC_API_KEY":              "",
		"CLAUDE_CODE_USE_BEDROCK":        "",
		"CLAUDE_CODE_USE_VERTEX":         "",
		"CLAUDE_CODE_USE_FOUNDRY":        "",
		"AWS_BEARER_TOKEN_BEDROCK":       "",
		"ANTHROPIC_CUSTOM_HEADERS":       "",
	}
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
	var userSettings map[string]any
	if !in.Selection.IsZero() && !in.Default {
		userSettings, _ = harness.ReadObject(filepath.Join(in.UserHome, ".claude", "settings.json"))
	}
	sibling := dedicatedLayer(in, path)
	s, explicit, err := resolveSlots(
		in.Table,
		in.Selection,
		tierSource(in, sibling, env, userSettings),
	)
	if err != nil {
		return harness.File{}, err
	}
	if sibling != nil {
		s = siblingModel(in.Table, sibling, cfg, s)
	}
	main, ids := wiring(in.Table, s)
	cfg["apiKeyHelper"] = APIKeyHelper(in.BaseURL)
	fresh := false
	if explicit {
		cfg["model"] = main
		if !in.Default && (layerSelectsModel(userSettings) || layerPinsTier(userSettings) ||
			layerHasCustomOption(userSettings)) {
			// Signal explicit launcher-only override with a blank ANTHROPIC_MODEL so aiven.settings.json wins over user
			// settings, including their model and slot pins.
			env["ANTHROPIC_MODEL"] = ""
		} else if v, ok := env["ANTHROPIC_MODEL"].(string); ok && bare(v) != "" {
			env["ANTHROPIC_MODEL"] = main
		} else if in.Default {
			if v, ok := env["ANTHROPIC_MODEL"].(string); ok && bare(v) == "" {
				delete(env, "ANTHROPIC_MODEL")
			}
		}
	} else if model, ok := cfg["model"].(string); cfg["model"] == nil || ok && bare(model) == "" {
		// A model the user chose in this file stays, and so does an ANTHROPIC_MODEL, which Claude Code runs over it. Setup
		// never sets ANTHROPIC_MODEL, since it would outrank the user's choice. A blank one chooses nothing.
		cfg["model"] = main
		fresh = true
	}
	if explicit || fresh {
		env[discoveryVar] = "1"
	}
	for k, v := range gatewayEnv(in.BaseURL, ids) {
		env[k] = v
	}
	if ids["CUSTOM"] != "" {
		env["ANTHROPIC_CUSTOM_MODEL_OPTION"] = ids["CUSTOM"]
	} else if explicit && !in.Default && layerHasCustomOption(userSettings) {
		// Blank an inherited user-level custom option so launcher-only preset/tier picks do not re-import it.
		env["ANTHROPIC_CUSTOM_MODEL_OPTION"] = ""
	} else if !keepsBlankCustomOption(in, env) {
		delete(env, "ANTHROPIC_CUSTOM_MODEL_OPTION")
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

// SelectedSetting returns the model a session started from data runs and the setting that chose it
// ("env.ANTHROPIC_MODEL", "model", or "").
func SelectedSetting(data []byte) (alias, setting string, err error) {
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", "", err
	}
	s, _, setting := pinnedFrom(nil, slots{}, layer{kind: layerProject, settings: cfg})
	return cmp.Or(running(s, s.main, nil), s.main), setting, nil
}

// Selected is the model a session started from the settings file data runs, through Claude Code's aliases: its
// env.ANTHROPIC_MODEL, else its model.
func Selected(data []byte) (string, error) {
	alias, _, err := SelectedSetting(data)
	return alias, err
}

// DefaultModel returns the main model alias that Claude Code setup selects by default for t.
func DefaultModel(t []models.Model) string { return defaultSlots(t).main }

// defaultPresets are the presets defaultSlots and Recommended defaults try, in order.
var defaultPresets = []models.PresetID{
	models.PresetAnthropic, models.PresetOpenAI, models.PresetGemini, models.PresetOpenWeight,
}

// defaultSlots keeps Claude Code on Claude models whenever Anthropic models are granted, falling back through
// curated presets (OpenAI, Gemini, Open-weight latest DeepSeek) otherwise. It never injects an unsolicited
// cross-vendor ANTHROPIC_CUSTOM_MODEL_OPTION.
func defaultSlots(t []models.Model) slots {
	for _, id := range defaultPresets {
		if p, ok := models.FindPreset(t, id); ok {
			return slots{main: p.Main, opus: p.Opus, sonnet: p.Sonnet, haiku: p.Haiku, fable: p.Fable}
		}
	}
	main := models.SonnetOrFirst(t)
	return slots{main: main, opus: main, sonnet: main, haiku: main, fable: main}
}

// RecommendedSelection is Recommended defaults as an explicit choice: the slots defaultSlots gives, replacing a
// model chosen earlier.
func RecommendedSelection(t []models.Model) harness.ModelSelection {
	for _, id := range defaultPresets {
		if _, ok := models.FindPreset(t, id); ok {
			return harness.ModelSelection{Preset: id}
		}
	}
	return harness.ModelSelection{Model: models.SonnetOrFirst(t)}
}

func keepGrantedClaudeTier(t []models.Model, env map[string]any, key, fallback string) string {
	if alias := grantedPin(t, env, key, ""); alias != "" && IsClaude(t, alias) {
		return alias
	}
	return fallback
}

func resolveSlots(
	t []models.Model,
	sel harness.ModelSelection,
	existingEnv map[string]any,
) (slots, bool, error) {
	if sel.IsZero() || len(t) == 0 {
		return keptSlots(t, existingEnv), false, nil
	}
	if len(sel.Models) > 0 {
		s, err := fillSlots(t, sel)
		return s, err == nil, err
	}
	if sel.Model != "" {
		return modelSlots(t, sel.Model, existingEnv)
	}
	p, ok := models.FindPreset(t, sel.Preset)
	if !ok {
		return slots{}, false, fmt.Errorf("preset %q has no granted models", sel.Preset)
	}
	return slots{
		main:   p.Main,
		opus:   p.Opus,
		sonnet: p.Sonnet,
		haiku:  p.Haiku,
		fable:  p.Fable,
	}, true, nil
}

// modelSlots fills the slots for a single chosen model.
func modelSlots(t []models.Model, model string, existingEnv map[string]any) (slots, bool, error) {
	chosen := bare(model)
	if p, ok := models.FindPreset(t, models.PresetAnthropic); ok {
		// A granted model of a tier's own keeps that tier (a claude-fable the preset does not
		// name); only a tier the key grants none of takes the preset's stand-in.
		own, stand := claudeTiers(
			t,
		), slots{
			opus:   p.Opus,
			sonnet: p.Sonnet,
			haiku:  p.Haiku,
			fable:  p.Fable,
		}
		s := slots{main: chosen}
		for _, v := range tierVars {
			s = s.withTier(v, keepGrantedClaudeTier(t, existingEnv, v, cmp.Or(own.tier(v), stand.tier(v))))
		}
		if chosen != s.opus && chosen != s.sonnet && chosen != s.haiku && chosen != s.fable {
			s.custom = chosen
		}
		return s, true, nil
	}
	return slots{
		main:   chosen,
		opus:   chosen,
		sonnet: chosen,
		haiku:  chosen,
		fable:  chosen,
	}, true, nil
}

// overlayJSON outranks native settings only for launcher-owned fields; it never holds the key.
func overlayJSON(
	baseURL, main string,
	ids map[string]string,
	name, window string,
	discovery bool,
) []byte {
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
	if discovery {
		env[discoveryVar] = "1"
	}
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

// childEnv is the child env for the gateway at baseURL, running main, with the credentials of other providers blanked.
func childEnv(baseURL, main string) []string {
	return []string{
		"ANTHROPIC_BASE_URL=" + baseURL,
		"ANTHROPIC_AUTH_TOKEN=",
		"ANTHROPIC_API_KEY=",
		"CLAUDE_CODE_USE_BEDROCK=",
		"CLAUDE_CODE_USE_VERTEX=",
		"CLAUDE_CODE_USE_FOUNDRY=",
		"AWS_BEARER_TOKEN_BEDROCK=",
		"ANTHROPIC_CUSTOM_HEADERS=",
		"ANTHROPIC_MODEL=" + main,
	}
}

// slotEnv is the child env of each tier slot, opus to fable, then the custom option.
func slotEnv(t []models.Model, s slots, ids map[string]string) []string {
	var out []string
	for _, v := range slices.Concat(tierVars[1:], tierVars[:1]) {
		out = append(out, slot(t, v, s.tier(v), ids[tierName(v)])...)
	}
	return append(out, slot(t, "ANTHROPIC_CUSTOM_MODEL_OPTION", s.custom, ids["CUSTOM"])...)
}

// Launch passes only launcher-owned settings inline. Native user/project settings remain additive;
// arbitrary extras in SettingsFile are retained by setup for direct native invocation.
func Launch(in Input) (harness.Plan, error) {
	cfg, err := configuredSettings(in.UserHome, in.BaseURL)
	if err != nil {
		return harness.Plan{}, err
	}
	s, err := withPins(in.Table, defaultSlots(in.Table), cfg, in.UserHome, in.WorkDir)
	if err != nil {
		return harness.Plan{}, err
	}
	main, ids := wiring(in.Table, s)
	// The window follows the model a native --model runs instead.
	name, window := windowVar(in.Table, running(s, s.main, in.Args))
	child := childEnv(in.BaseURL, main)
	w := windowEnv(name, window)
	child = append(
		child,
		maxContextTokens+"="+w[maxContextTokens],
		autoCompactWindow+"="+w[autoCompactWindow],
	)
	child = append(child, slotEnv(in.Table, s, ids)...)
	discovery := discoveryOn(cfg)
	if discovery {
		child = append(child, discoveryVar+"=1")
	}
	var inline map[string]any
	if err := json.Unmarshal(
		overlayJSON(in.BaseURL, main, ids, name, window, discovery),
		&inline,
	); err != nil {
		return harness.Plan{}, err
	}
	inline["apiKeyHelper"] = APIKeyHelper(in.BaseURL)
	// One line, since cmd.exe cannot pass a newline to a batch file such as npm's claude.cmd.
	settings, err := json.Marshal(inline)
	if err != nil {
		return harness.Plan{}, err
	}
	return harness.Plan{
		Argv: append([]string{"claude", "--settings", string(settings)}, in.Args...),
		Env:  child,
	}, nil
}
