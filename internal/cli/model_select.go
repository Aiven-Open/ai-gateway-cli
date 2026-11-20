// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/omp"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/opencode"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/pi"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

type configuredModel struct {
	present      bool
	granted      bool
	unrepairable bool
	file         string
	setting      string
	model        string
}

func inspectConfiguredModel(
	tool string,
	env config.Env,
	table []models.Model,
	snapshot, plan0 harness.Plan,
) configuredModel {
	switch tool {
	case "codex":
		if len(snapshot.Files) < 2 {
			return configuredModel{}
		}
		var file, setting, model string
		if snapshot.Files[1].Existed {
			var profile map[string]any
			if err := toml.Unmarshal(snapshot.Files[1].Before, &profile); err == nil {
				if m, ok := profile["model"].(string); ok && m != "" {
					file, setting, model = snapshot.Files[1].Path, "model", m
				}
			}
		}
		if model == "" && snapshot.Files[0].Existed {
			var cfg map[string]any
			if err := toml.Unmarshal(snapshot.Files[0].Before, &cfg); err == nil {
				if profiles, ok := cfg["profiles"].(map[string]any); ok {
					if legacy, ok := profiles["aiven"].(map[string]any); ok {
						if m, ok := legacy["model"].(string); ok && m != "" {
							file, setting, model = snapshot.Files[0].Path, "profiles.aiven.model", m
						}
					}
				}
			}
		}
		if model == "" {
			return configuredModel{}
		}
		_, granted := models.Find(table, model)
		return configuredModel{
			present: true,
			granted: granted,
			file:    file,
			setting: setting,
			model:   model,
		}
	case "claude":
		planned := make(map[string][]byte, len(plan0.Files))
		for _, f := range plan0.Files {
			planned[f.Path] = f.Data
		}
		snapByPath := make(map[string]harness.File, len(snapshot.Files))
		for _, f := range snapshot.Files {
			snapByPath[f.Path] = f
		}
		// Check read-only project layers first: an ungranted project model cannot be repaired by setup,
		// even when an ungranted user-level ANTHROPIC_MODEL would otherwise mask it during Effective resolution.
		if projPath, projAlias, projSetting, bad := claude.ProjectUngranted(
			table,
			env.WorkDir,
		); bad {
			return configuredModel{
				present:      true,
				granted:      false,
				unrepairable: true,
				file:         projPath,
				setting:      projSetting,
				model:        projAlias,
			}
		}
		// Check effective layered model (covers dedicated, ~/.claude/settings.json and project layers).
		if effPath, effAlias, err := claude.Effective(
			table,
			env.UserHome,
			env.WorkDir,
			planned,
		); err == nil &&
			effAlias != "" {
			_, granted := models.Find(table, effAlias)
			if !granted {
				setting := claudeFileSetting(effPath, snapByPath)
				if setting == "" {
					setting = "model"
				}
				unrepairable := false
				if env.WorkDir != "" {
					projShared := filepath.Join(env.WorkDir, ".claude", "settings.json")
					projLocal := filepath.Join(env.WorkDir, ".claude", "settings.local.json")
					unrepairable = effPath == projShared || effPath == projLocal
				}
				return configuredModel{
					present:      true,
					granted:      false,
					unrepairable: unrepairable,
					file:         effPath,
					setting:      setting,
					model:        effAlias,
				}
			}
			if effPath != "" {
				snap, inSnap := snapByPath[effPath]
				existed := !inSnap || snap.Existed
				if existed {
					setting := claudeFileSetting(effPath, snapByPath)
					if setting != "" {
						return configuredModel{
							present: true,
							granted: true,
							file:    effPath,
							setting: setting,
							model:   effAlias,
						}
					}
				}
			}
		}
		// Also inspect every existing snapshot file (dedicated + user settings.json) for ungranted models.
		var firstGranted configuredModel
		for _, snap := range snapshot.Files {
			if !snap.Existed {
				continue
			}
			setting := claudeExplicitSetting(snap.Before)
			if setting == "" {
				continue
			}
			effective, err := claude.Selected(harness.Strip(snap.Before, harness.Strict))
			if err != nil || effective == "" {
				continue
			}
			_, granted := models.Find(table, effective)
			cur := configuredModel{
				present: true,
				granted: granted,
				file:    snap.Path,
				setting: setting,
				model:   effective,
			}
			if !granted {
				return cur
			}
			if !firstGranted.present {
				firstGranted = cur
			}
		}
		return firstGranted
	}
	return configuredModel{}
}

func claudeFileSetting(path string, snapByPath map[string]harness.File) string {
	if snap, ok := snapByPath[path]; ok && snap.Existed {
		return claudeExplicitSetting(snap.Before)
	}
	if data, _, existed, err := inspectFile(path); err == nil && existed {
		return claudeExplicitSetting(data)
	}
	return ""
}

func claudeExplicitSetting(before []byte) string {
	var cfg map[string]any
	if err := json.Unmarshal(harness.Strip(before, harness.Strict), &cfg); err != nil {
		return ""
	}
	if env, ok := cfg["env"].(map[string]any); ok {
		if v, ok := env["ANTHROPIC_MODEL"].(string); ok && strings.TrimSpace(v) != "" {
			if sel, err := claude.Selected(
				harness.JSON(map[string]any{"env": map[string]any{"ANTHROPIC_MODEL": v}}),
			); err == nil &&
				sel != "" {
				return "env.ANTHROPIC_MODEL"
			}
		}
	}
	if v, ok := cfg["model"].(string); ok && strings.TrimSpace(v) != "" {
		if sel, err := claude.Selected(
			harness.JSON(map[string]any{"model": v}),
		); err == nil &&
			sel != "" {
			return "model"
		}
	}
	return ""
}

// shortHomePath renders path with a leading "~" when it lies under userHome.
func shortHomePath(userHome, path string) string {
	if userHome == "" || path == "" {
		return path
	}
	rel, err := filepath.Rel(userHome, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return "~/" + filepath.ToSlash(rel)
}

// toolBadge returns the Beat 1 live status badge for tool using read-only snapshot & gateway state.
func toolBadge(
	tool string,
	env config.Env,
	table []models.Model,
	existing configuredModel,
) string {
	if existing.present && !existing.granted {
		return fmt.Sprintf(
			"! ungranted: %s in %s",
			safe(existing.model),
			shortHomePath(env.UserHome, existing.file),
		)
	}
	if existing.present && existing.granted {
		return fmt.Sprintf("configured (%s)", safe(existing.model))
	}
	switch tool {
	case "pi":
		if _, err := pi.Ready(env.UserHome, env.BaseURL); err == nil {
			if pref, err := config.LoadModelPref(env.UserHome, env.BaseURL, "pi"); err == nil &&
				pref != "" {
				return fmt.Sprintf("configured (%s)", safe(pref))
			}
			return "configured"
		}
	case "opencode":
		if _, err := opencode.Ready(env.UserHome, env.ConfigHome, env.BaseURL); err == nil {
			if pref, err := config.LoadModelPref(env.UserHome, env.BaseURL, "opencode"); err == nil &&
				pref != "" {
				return fmt.Sprintf("configured (%s)", safe(pref))
			}
			return "configured"
		}
	case "omp":
		if _, err := omp.Ready(env.UserHome, env.BaseURL); err == nil {
			if pref, err := config.LoadModelPref(env.UserHome, env.BaseURL, "omp"); err == nil &&
				pref != "" {
				return fmt.Sprintf("configured (%s)", safe(pref))
			}
			return "configured"
		}
	case "claude":
		if err := claude.Ready(env.UserHome, env.BaseURL); err == nil {
			return "configured"
		}
	}
	return "new"
}

// resolvedToolModel returns the effective model alias for tool given its planned config and selection.
func resolvedToolModel(
	tool string,
	env config.Env,
	table []models.Model,
	plan harness.Plan,
	sel harness.ModelSelection,
) string {
	switch tool {
	case "claude":
		planned := make(map[string][]byte, len(plan.Files))
		for _, f := range plan.Files {
			planned[f.Path] = f.Data
		}
		if _, alias, err := claude.Effective(table, env.UserHome, env.WorkDir, planned); err == nil &&
			alias != "" {
			return alias
		}
		if len(plan.Files) > 0 {
			if alias, err := claude.Selected(plan.Files[0].Data); err == nil && alias != "" {
				return alias
			}
		}
	case "codex":
		if len(plan.Files) > 0 {
			var cfg map[string]any
			if err := toml.Unmarshal(plan.Files[len(plan.Files)-1].Data, &cfg); err == nil {
				if m, _ := cfg["model"].(string); m != "" {
					return m
				}
			}
		}
		return models.CodexOrFirst(table)
	case "pi", "opencode", "omp":
		if chosen, err := sel.ResolveMain(table); err == nil && chosen != "" {
			return chosen
		}
		if pref, err := config.LoadModelPref(env.UserHome, env.BaseURL, tool); err == nil && pref != "" {
			if _, ok := models.Find(table, pref); ok {
				return pref
			}
		}
		return models.SonnetOrFirst(table)
	}
	return models.SonnetOrFirst(table)
}

func nativeDefaultFile(tool string, env config.Env) string {
	switch tool {
	case "claude":
		return "~/.claude/settings.json"
	case "codex":
		return "~/.codex/config.toml"
	case "pi":
		return "~/.pi/agent/settings.json"
	case "opencode":
		if env.ConfigHome != "" {
			return filepath.Join(env.ConfigHome, "opencode", "opencode.json")
		}
		return "~/.config/opencode/opencode.json"
	case "omp":
		return "~/.omp/agent/config.yml"
	}
	return ""
}

type modelMenuItem struct {
	option       option
	refuse       bool
	custom       bool
	customize    bool
	fixConflicts bool
	sel          harness.ModelSelection
}

type multiModelDecision struct {
	shared       harness.ModelSelection
	perTool      map[string]harness.ModelSelection
	customize    bool
	summaryLabel string
}

// selectMultiToolModels runs Beat 2 when 2+ tools are selected, resolving ungranted conflicts inline
// alongside presets rather than forcing [1/N]..[N/N] prompts across healthy tools.
func selectMultiToolModels(
	deps Deps,
	p presenter,
	env config.Env,
	eligible []*setupTarget,
	table []models.Model,
	allowBack bool,
) (multiModelDecision, error) {
	var conflicted []*setupTarget
	fixMap := map[string]harness.ModelSelection{}
	var fixParts []string
	allFixable := true
	for _, t := range eligible {
		if t.existing.present && !t.existing.granted {
			conflicted = append(conflicted, t)
			if alias := models.ProviderAlias(table, t.existing.model); alias != "" {
				fixMap[t.out.tool] = harness.ModelSelection{Model: alias}
				fixParts = append(fixParts, fmt.Sprintf("%s -> %s", toolLabel(t.out.tool), safe(alias)))
			} else {
				allFixable = false
			}
		}
	}

	var items []modelMenuItem
	if len(conflicted) > 0 {
		for _, t := range conflicted {
			p.ui.railWarn(fmt.Sprintf(
				"%s: %s (%s) selects %s, which this key does not grant.",
				toolLabel(t.out.tool),
				shortHomePath(env.UserHome, t.existing.file),
				t.existing.setting,
				t.existing.model,
			))
		}
		if allFixable && len(fixParts) > 0 {
			items = append(items, modelMenuItem{
				option: option{
					label:  "Switch " + strings.Join(fixParts, ", ") + " & use Recommended defaults",
					detail: recommendedDefaultsDetail(eligible, table),
				},
				fixConflicts: true,
			})
		} else {
			items = append(items, modelMenuItem{
				option: option{
					label: "Leave unchanged (cancel setup)",
					detail: "refuse setup without changing " + shortHomePath(
						env.UserHome,
						conflicted[0].existing.file,
					),
				},
				refuse: true,
			})
		}
		for _, pr := range models.Presets(table) {
			items = append(items, modelMenuItem{
				option: option{label: pr.Name, detail: multiPresetDetail(pr, eligible, table)},
				sel:    harness.ModelSelection{Preset: pr.ID},
			})
		}
		items = append(items, modelMenuItem{
			option:    option{label: "Customize per tool…", detail: "pick separately for each tool"},
			customize: true,
		})
		if allFixable && len(fixParts) > 0 {
			items = append(items, modelMenuItem{
				option: option{
					label: "Leave unchanged (cancel setup)",
					detail: "refuse setup without changing " + shortHomePath(
						env.UserHome,
						conflicted[0].existing.file,
					),
				},
				refuse: true,
			})
		}
	} else {
		items = append(items, modelMenuItem{
			option: option{
				label:  "Recommended defaults",
				detail: recommendedDefaultsDetail(eligible, table),
			},
		})
		for _, pr := range models.Presets(table) {
			items = append(items, modelMenuItem{
				option: option{label: pr.Name, detail: multiPresetDetail(pr, eligible, table)},
				sel:    harness.ModelSelection{Preset: pr.ID},
			})
		}
		items = append(items, modelMenuItem{
			option:    option{label: "Customize per tool…", detail: "pick separately for each tool"},
			customize: true,
		})
	}

	subtitle := multiToolSubtitle(eligible, table)
	var pickedIdx int
	if p.inline != nil {
		c := choice{
			title:     "Models",
			subtitle:  subtitle,
			allowBack: allowBack,
			rail:      true,
			theme:     &p.ui.th,
		}
		for _, it := range items {
			c.options = append(c.options, it.option)
		}
		picked, err := p.inline.Select(c)
		if err != nil {
			return multiModelDecision{}, err
		}
		pickedIdx = picked[0]
	} else {
		var err error
		pickedIdx, err = promptModelMenuPlain(
			deps,
			p,
			"Models for selected tools",
			subtitle,
			items,
			allowBack,
		)
		if err != nil {
			return multiModelDecision{}, err
		}
	}
	chosen := items[pickedIdx]
	if chosen.refuse {
		c0 := conflicted[0]
		return multiModelDecision{}, notGranted(c0.existing.file, c0.existing.model, table)
	}
	if chosen.customize {
		return multiModelDecision{customize: true}, nil
	}
	if chosen.fixConflicts {
		return multiModelDecision{
			perTool:      fixMap,
			summaryLabel: chosen.option.label,
		}, nil
	}
	return multiModelDecision{
		shared:       chosen.sel,
		summaryLabel: chosen.option.label,
	}, nil
}

func multiToolSubtitle(eligible []*setupTarget, table []models.Model) string {
	var names []string
	hasCatalogueTool := false
	for _, t := range eligible {
		names = append(names, toolLabel(t.out.tool))
		if t.out.tool == "pi" || t.out.tool == "opencode" || t.out.tool == "omp" {
			hasCatalogueTool = true
		}
	}
	sub := strings.Join(names, ", ")
	if hasCatalogueTool {
		sub += fmt.Sprintf(" · Pi/OpenCode/OMP register all %d granted models", len(table))
	}
	return sub
}

func multiPresetDetail(pr models.Preset, eligible []*setupTarget, table []models.Model) string {
	hasClaude := slices.ContainsFunc(
		eligible,
		func(t *setupTarget) bool { return t.out.tool == "claude" },
	)
	if hasClaude {
		return safe(presetDetail(pr, "claude"))
	}
	return safe(pr.Main)
}

func recommendedDefaultsDetail(eligible []*setupTarget, table []models.Model) string {
	hasCodex := slices.ContainsFunc(
		eligible,
		func(t *setupTarget) bool { return t.out.tool == "codex" },
	)
	sonnet := models.SonnetOrFirst(table)
	codexModel := models.CodexOrFirst(table)
	if hasCodex && codexModel != "" && sonnet != "" && codexModel != sonnet {
		return fmt.Sprintf("keep existing; defaults: %s / Codex: %s", safe(sonnet), safe(codexModel))
	}
	if sonnet != "" {
		return "keep existing; default: " + safe(sonnet)
	}
	return "keep existing granted models; apply defaults"
}

func buildModelMenu(
	tool string,
	env config.Env,
	table []models.Model,
	existing configuredModel,
	index, total int,
) (title, subtitle string, items []modelMenuItem) {
	prefix := ""
	if total > 1 {
		prefix = fmt.Sprintf("[%d/%d] ", index, total)
	}
	switch tool {
	case "claude":
		title = "Model preset for Claude Code"
		subtitle = "Sets default model and opus/sonnet/haiku slots"
	case "codex":
		title = "Default model for Codex"
	default:
		title = "Default model for " + toolLabel(tool)
		subtitle = fmt.Sprintf(
			"Registers all %d granted models; sets default for launch %s",
			len(table),
			tool,
		)
	}
	if existing.present && !existing.granted {
		title = fmt.Sprintf("%s%s (current %s is not granted)", prefix, title, safe(existing.model))
		items = append(items, modelMenuItem{
			option: option{
				label:  "Keep unchanged (refuse setup)",
				detail: "leave " + safe(shortHomePath(env.UserHome, existing.file)) + " as it is",
			},
			refuse: true,
		})
		if alias := models.ProviderAlias(table, existing.model); alias != "" {
			items = append(items, modelMenuItem{
				option: option{
					label:  fmt.Sprintf("Switch to %s (matches %s)", safe(alias), safe(existing.model)),
					detail: modelDisplayDetail(table, alias),
				},
				sel: harness.ModelSelection{Model: alias},
			})
		}
	} else {
		title = prefix + title
		if existing.present && existing.granted {
			items = append(items, modelMenuItem{
				option: option{
					label:  fmt.Sprintf("Keep current (%s)", safe(existing.model)),
					detail: "leave current model unchanged",
				},
			})
		}
	}
	for _, pr := range models.Presets(table) {
		items = append(items, modelMenuItem{
			option: option{label: pr.Name, detail: safe(presetDetail(pr, tool))},
			sel:    harness.ModelSelection{Preset: pr.ID},
		})
	}
	if n := countCustomModels(table); n > 0 {
		items = append(items, modelMenuItem{
			option: option{
				label:  "Custom model…",
				detail: fmt.Sprintf("pick from %d granted models", n),
			},
			custom: true,
		})
	}
	return title, subtitle, items
}

func modelDisplayDetail(table []models.Model, alias string) string {
	if m, ok := models.Find(table, alias); ok {
		if name := safe(m.DisplayName()); name != "" && name != safe(alias) {
			return name
		}
	}
	return ""
}

func presetDetail(pr models.Preset, tool string) string {
	if tool == "claude" {
		if pr.ID == models.PresetAnthropic && (pr.Opus != pr.Main || pr.Haiku != pr.Main) {
			return fmt.Sprintf("%s (opus: %s, haiku: %s)", pr.Main, pr.Opus, pr.Haiku)
		}
		if pr.Haiku != "" && pr.Haiku != pr.Main {
			return fmt.Sprintf("%s (fast: %s)", pr.Main, pr.Haiku)
		}
	}
	return pr.Main
}

func selectToolModel(
	deps Deps,
	p presenter,
	env config.Env,
	tool string,
	table []models.Model,
	existing configuredModel,
	index, total int,
	allowBack bool,
) (harness.ModelSelection, string, error) {
	if existing.present && !existing.granted {
		p.ui.railWarn(fmt.Sprintf("%s: %s (%s) selects %s, which this key does not grant.",
			toolLabel(tool), existing.file, existing.setting, existing.model))
	}
	title, subtitle, items := buildModelMenu(tool, env, table, existing, index, total)
	if len(items) == 0 {
		return harness.ModelSelection{}, "", nil
	}
	for {
		var pickedIdx int
		if p.inline != nil {
			c := choice{
				title:     title,
				subtitle:  subtitle,
				allowBack: allowBack,
				rail:      true,
				theme:     &p.ui.th,
			}
			for _, it := range items {
				c.options = append(c.options, it.option)
			}
			picked, err := p.inline.Select(c)
			if err != nil {
				return harness.ModelSelection{}, "", err
			}
			pickedIdx = picked[0]
		} else {
			var err error
			pickedIdx, err = promptModelMenuPlain(deps, p, title, subtitle, items, allowBack)
			if err != nil {
				return harness.ModelSelection{}, "", err
			}
		}
		chosen := items[pickedIdx]
		if chosen.refuse {
			return harness.ModelSelection{}, "", notGranted(existing.file, existing.model, table)
		}
		if !chosen.custom {
			return chosen.sel, chosen.option.label, nil
		}
		alias, err := pickSpecificModel(deps, p, tool, table)
		if errors.Is(err, errBack) {
			continue
		}
		if err != nil {
			return harness.ModelSelection{}, "", err
		}
		return harness.ModelSelection{Model: alias}, alias, nil
	}
}

func promptModelMenuPlain(
	deps Deps,
	p presenter,
	title, subtitle string,
	items []modelMenuItem,
	allowBack bool,
) (int, error) {
	p.ui.line(2, title+":")
	if subtitle != "" {
		p.ui.note(2, subtitle)
	}
	for i, it := range items {
		line := fmt.Sprintf("%d) %s", i+1, it.option.label)
		if it.option.detail != "" {
			line += " (" + it.option.detail + ")"
		}
		p.ui.line(4, line)
	}
	prompt := "Choose model option (Enter for 1): "
	if allowBack {
		prompt = `Choose model option (Enter for 1, or "back"): `
	}
	for range 3 {
		answer, err := deps.Prompter.ReadLine(prompt)
		if errors.Is(err, io.EOF) || errors.Is(err, errInputEnded) {
			return 0, errInputEnded
		}
		if err != nil {
			return 0, err
		}
		trimmed := strings.TrimSpace(answer)
		if trimmed == "" {
			return 0, nil
		}
		if allowBack && (strings.EqualFold(trimmed, "b") || strings.EqualFold(trimmed, "back")) {
			return 0, errBack
		}
		if n, err := strconv.Atoi(trimmed); err == nil && n >= 1 && n <= len(items) {
			return n - 1, nil
		}
		p.ui.line(2, fmt.Sprintf("Choose a number from 1 to %d.", len(items)))
	}
	return 0, errors.New("no answer: expected model option number")
}

func customModelGroups(table []models.Model) []models.Group {
	var nonRouter []models.Model
	for _, m := range table {
		if m.Alias != models.Router {
			nonRouter = append(nonRouter, m)
		}
	}
	if len(nonRouter) == 0 {
		return nil
	}
	return models.Grouped(nonRouter)
}

func countCustomModels(table []models.Model) int {
	n := 0
	for _, m := range table {
		if m.Alias != models.Router {
			n++
		}
	}
	return n
}

func pickSpecificModel(
	deps Deps,
	p presenter,
	tool string,
	table []models.Model,
) (string, error) {
	groups := customModelGroups(table)
	var flat []models.Model
	for _, g := range groups {
		flat = append(flat, g.Models...)
	}
	if len(flat) == 0 {
		return "", errors.New("no models available to choose")
	}
	if p.inline != nil {
		c := choice{
			title:         "Select a model for " + toolLabel(tool),
			allowBack:     true,
			filterDefault: true,
			rail:          true,
			theme:         &p.ui.th,
		}
		for _, g := range groups {
			for _, m := range g.Models {
				detail := safe(g.Name)
				if name := safe(m.DisplayName()); name != "" && name != safe(m.Alias) {
					detail = name + " · " + detail
				}
				c.options = append(c.options, option{label: safe(m.Alias), detail: detail})
			}
		}
		picked, err := p.inline.Select(c)
		if err != nil {
			return "", err
		}
		return flat[picked[0]].Alias, nil
	}
	p.ui.line(2, "Select a model for "+toolLabel(tool)+":")
	idx := 0
	for _, g := range groups {
		p.ui.line(4, g.Name)
		for _, m := range g.Models {
			idx++
			entry := fmt.Sprintf("%d) %s", idx, m.Alias)
			if name := safe(m.DisplayName()); name != "" && name != safe(m.Alias) {
				entry += " (" + name + ")"
			}
			p.ui.line(6, entry)
		}
	}
	for range 3 {
		answer, err := deps.Prompter.ReadLine(`Choose model number or id (or "back"): `)
		if errors.Is(err, io.EOF) || errors.Is(err, errInputEnded) {
			return "", errInputEnded
		}
		if err != nil {
			return "", err
		}
		trimmed := strings.TrimSpace(answer)
		if strings.EqualFold(trimmed, "b") || strings.EqualFold(trimmed, "back") {
			return "", errBack
		}
		if n, err := strconv.Atoi(trimmed); err == nil && n >= 1 && n <= len(flat) {
			return flat[n-1].Alias, nil
		}
		for _, m := range flat {
			if m.Alias == trimmed {
				return m.Alias, nil
			}
		}
		p.ui.line(2, "Choose a number or model id from the list.")
	}
	return "", errors.New("no answer: expected model number or id")
}

// selectDirectInvocation runs Beat 3 in plain English, allowing step-back to Beat 2.
func selectDirectInvocation(
	deps Deps,
	p presenter,
	env config.Env,
	eligible []*setupTarget,
) (string, error) {
	if len(eligible) == 0 {
		return "none", nil
	}
	if len(eligible) == 1 {
		t := eligible[0]
		tool := t.out.tool
		nativeFile := nativeDefaultFile(tool, env)
		yesDetail := "updates " + nativeFile
		if tool == "claude" && t.existing.present && !t.existing.granted &&
			t.existing.file == filepath.Join(env.UserHome, ".claude", "settings.json") {
			yesDetail += " (fixes ungranted model)"
		}
		opts := []option{
			{
				label:  fmt.Sprintf("Yes — make Aiven the default for %s", toolLabel(tool)),
				detail: yesDetail,
			},
			{
				label:  fmt.Sprintf("No — only via 'aiven-ai-gateway launch %s'", tool),
				detail: "keeps existing " + nativeFile + " default",
			},
		}
		pickedIdx, err := chooseDirectOption(
			deps,
			p,
			fmt.Sprintf("Use Aiven AI Gateway as the default for %s?", toolLabel(tool)),
			opts,
		)
		if err != nil {
			return "", err
		}
		t.out.asDefault = pickedIdx == 0
		if t.out.asDefault {
			return fmt.Sprintf("%s (%s)", toolLabel(tool), nativeFile), nil
		}
		return "launch command only", nil
	}

	opts := []option{
		{
			label:  fmt.Sprintf("Yes — all %d tools", len(eligible)),
			detail: "running tools directly uses Aiven automatically",
		},
		{
			label:  "No — only via 'aiven-ai-gateway launch'",
			detail: "keeps your current defaults",
		},
		{
			label:  "Choose per tool…",
			detail: "pick which tool configs to update",
		},
	}
	for {
		pickedIdx, err := chooseDirectOption(deps, p, "Use Aiven AI Gateway as the default?", opts)
		if err != nil {
			return "", err
		}
		switch pickedIdx {
		case 0:
			for _, t := range eligible {
				t.out.asDefault = true
			}
			return fmt.Sprintf("all %d tools", len(eligible)), nil
		case 1:
			for _, t := range eligible {
				t.out.asDefault = false
			}
			return "launch command only", nil
		case 2:
			summary, err := selectDirectPerTool(deps, p, env, eligible)
			if errors.Is(err, errBack) {
				continue
			}
			return summary, err
		}
	}
}

func chooseDirectOption(deps Deps, p presenter, title string, opts []option) (int, error) {
	if p.inline != nil {
		c := choice{
			title:     title,
			options:   opts,
			allowBack: true,
			rail:      true,
			theme:     &p.ui.th,
		}
		picked, err := p.inline.Select(c)
		if err != nil {
			return 0, err
		}
		return picked[0], nil
	}
	p.ui.line(2, title)
	for i, o := range opts {
		line := fmt.Sprintf("%d) %s", i+1, o.label)
		if o.detail != "" {
			line += " (" + o.detail + ")"
		}
		p.ui.line(4, line)
	}
	for range 3 {
		answer, err := deps.Prompter.ReadLine(
			`Choose default-provider option (Enter for 1, or "back"): `,
		)
		if errors.Is(err, io.EOF) || errors.Is(err, errInputEnded) {
			return 0, errInputEnded
		}
		if err != nil {
			return 0, err
		}
		trimmed := strings.ToLower(strings.TrimSpace(answer))
		switch trimmed {
		case "", "y", "yes":
			return 0, nil
		case "n", "no":
			return 1, nil
		case "b", "back":
			return 0, errBack
		}
		if n, err := strconv.Atoi(trimmed); err == nil && n >= 1 && n <= len(opts) {
			return n - 1, nil
		}
		p.ui.line(2, fmt.Sprintf("Choose a number from 1 to %d.", len(opts)))
	}
	return 0, errors.New("no answer: expected default-provider option")
}

func selectDirectPerTool(
	deps Deps,
	p presenter,
	env config.Env,
	eligible []*setupTarget,
) (string, error) {
	if p.inline != nil {
		c := choice{
			title:     "Default provider per tool",
			multi:     true,
			allowBack: true,
			rail:      true,
			theme:     &p.ui.th,
		}
		for _, t := range eligible {
			detail := nativeDefaultFile(t.out.tool, env)
			if t.out.tool == "claude" && t.existing.present && !t.existing.granted &&
				t.existing.file == filepath.Join(env.UserHome, ".claude", "settings.json") {
				detail += " (fixes ungranted model)"
			}
			c.options = append(c.options, option{
				label:  toolLabel(t.out.tool),
				detail: detail,
				picked: t.out.asDefault,
			})
		}
		picked, err := p.inline.Select(c)
		if err != nil {
			return "", err
		}
		pickedSet := map[int]bool{}
		for _, i := range picked {
			pickedSet[i] = true
		}
		var names []string
		for i, t := range eligible {
			t.out.asDefault = pickedSet[i]
			if t.out.asDefault {
				names = append(names, toolLabel(t.out.tool))
			}
		}
		if len(names) == 0 {
			return "via 'aiven-ai-gateway launch <tool>'", nil
		}
		return strings.Join(names, ", ") + " directly", nil
	}
	var names []string
	offered := make([]string, len(eligible))
	p.ui.line(2, "Choose tools to use Aiven AI Gateway as the default:")
	for i, t := range eligible {
		offered[i] = t.out.tool
		p.ui.line(
			4,
			fmt.Sprintf("%d) %s (%s)", i+1, toolLabel(t.out.tool), nativeDefaultFile(t.out.tool, env)),
		)
	}
	for range 3 {
		answer, err := deps.Prompter.ReadLine(
			`Numbers or names separated by commas, "all", Enter for none, or "back": `,
		)
		if errors.Is(err, io.EOF) || errors.Is(err, errInputEnded) {
			return "", errInputEnded
		}
		if err != nil {
			return "", err
		}
		trimmed := strings.TrimSpace(answer)
		if strings.EqualFold(trimmed, "b") || strings.EqualFold(trimmed, "back") {
			return "", errBack
		}
		picked, ok := parsePick(trimmed, offered)
		if !ok {
			p.ui.line(2, "Choose from the list, for example 1,2 or pi,codex.")
			continue
		}
		for _, t := range eligible {
			t.out.asDefault = slices.Contains(picked, t.out.tool)
			if t.out.asDefault {
				names = append(names, toolLabel(t.out.tool))
			}
		}
		if len(names) == 0 {
			return "via 'aiven-ai-gateway launch <tool>'", nil
		}
		return strings.Join(names, ", ") + " directly", nil
	}
	return "", errors.New("no answer: expected tool numbers or names")
}
