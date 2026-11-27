// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

// Copy of Claude Code's model lists, checked against the Ditto styleguide.
const (
	claudeLimitNote = "Claude models are always available. " +
		"Add up to five models from other vendors."
	claudeLimitStatus = "Up to %[2]d models from other vendors, %[1]d chosen. " +
		"Claude models do not count."
	claudeLimitRefusal = "Five models from other vendors are already chosen. Clear one to add another."
	claudePlainLimit   = "Choose up to five models from other vendors. Claude models do not count."
	claudeNoneChosen   = "Choose at least one model."
	claudeHaikuWarning = "Claude Code also sends background requests to the haiku tier: they go to %s."
	claudeNoTierNote   = "Not in a tier, choose with /model in Claude Code: "
	claudeKeptNote     = "Chosen so far, Enter keeps them: "
	claudePlainPrompt  = "Numbers or ids separated by commas, or b to go back: "
)

// claudeMenu lists Claude Code's choices: an ungranted current model's way out first, Keep current when the
// current model is granted, then Recommended defaults and Choose models.
func claudeMenu(
	title string,
	env config.Env,
	table []models.Model,
	existing configuredModel,
) (string, []modelMenuItem) {
	title, items := ungrantedItems(title, env, table, existing)
	if existing.present && existing.granted {
		items = append(items, modelMenuItem{option: option{
			label: "Keep current", detail: safe(existing.model),
		}})
	}
	recommended := claude.RecommendedSelection(table)
	main, _ := recommended.ResolveMain(table)
	return title, append(
		items,
		modelMenuItem{
			sel:    recommended,
			option: option{label: "Recommended defaults", detail: safe(main)},
		},
		modelMenuItem{custom: true, option: option{
			label:  "Choose models",
			detail: fmt.Sprintf("pick from %d granted models", len(table)),
		}},
	)
}

// ungrantedItems is the title and the first menu items for a configured model the key does not grant: Keep
// unchanged, which refuses setup, and a switch to the granted alias that matches it. Both menus use it.
func ungrantedItems(
	title string,
	env config.Env,
	table []models.Model,
	existing configuredModel,
) (string, []modelMenuItem) {
	if !existing.present || existing.granted {
		return title, nil
	}
	title = fmt.Sprintf("%s (current %s is not granted)", title, safe(existing.model))
	items := []modelMenuItem{{
		refuse: true,
		option: option{
			label:  "Keep unchanged (refuse setup)",
			detail: "leave " + safe(shortHomePath(env.UserHome, existing.file)) + " as it is",
		},
	}}
	if alias := models.ProviderAlias(table, existing.model); alias != "" {
		items = append(items, modelMenuItem{
			sel: harness.ModelSelection{Model: alias},
			option: option{
				label:  fmt.Sprintf("Switch to %s (matches %s)", safe(alias), safe(existing.model)),
				detail: modelDisplayDetail(table, alias),
			},
		})
	}
	return title, items
}

// selectClaudeModels is selectToolModel for Claude Code: its menu, then the model and default lists.
func selectClaudeModels(
	deps Deps,
	p presenter,
	env config.Env,
	table []models.Model,
	existing configuredModel,
	index, total int,
	allowBack bool,
) (harness.ModelSelection, string, error) {
	title := "Models for Claude Code"
	if total > 1 {
		title = fmt.Sprintf("[%d/%d] %s", index, total, title)
	}
	title, items := claudeMenu(title, env, table, existing)
	opts := make([]option, len(items))
	for i, it := range items {
		opts[i] = it.option
	}
	for {
		picked, err := selectMenuOption(deps, p, title, "", opts, 0, allowBack)
		if err != nil {
			return harness.ModelSelection{}, "", err
		}
		it := items[picked]
		if it.refuse {
			return harness.ModelSelection{}, "", notGranted(existing.file, existing.model, table)
		}
		if !it.custom {
			return it.sel, it.option.label, nil
		}
		chosen, err := chooseClaudeModels(deps, p, table)
		if errors.Is(err, errBack) {
			continue
		}
		if err != nil {
			return harness.ModelSelection{}, "", err
		}
		return chosen, claudeLabel(chosen), nil
	}
}

// claudeLabel names a Choose models answer in the step summary: the default, and how many came with it.
func claudeLabel(sel harness.ModelSelection) string {
	if len(sel.Models) <= 1 {
		return safe(sel.Model)
	}
	return fmt.Sprintf("%s and %d more", safe(sel.Model), len(sel.Models)-1)
}

// chooseClaudeModels asks for the models, then for the default among them. Back from the default list returns to
// the models with the picks kept.
func chooseClaudeModels(
	deps Deps,
	p presenter,
	table []models.Model,
) (harness.ModelSelection, error) {
	var picks []string
	for {
		var err error
		if picks, err = pickClaudeModels(deps, p, table, picks); err != nil {
			return harness.ModelSelection{}, err
		}
		main, err := pickClaudeDefault(deps, p, picks)
		if errors.Is(err, errBack) {
			continue
		}
		if err != nil {
			return harness.ModelSelection{}, err
		}
		sel := harness.ModelSelection{Model: main, Models: picks}
		notes, warning := claudeSlotNotes(table, sel)
		for _, n := range notes {
			p.ui.railNote(n)
		}
		if warning != "" {
			p.ui.railWarn(warning)
		}
		return sel, nil
	}
}

// claudeSlotNotes says which Claude Code slot each chosen model from another vendor takes and which chosen Claude
// models take none, then warns when the haiku tier goes to another vendor. Choose models and Review print them.
func claudeSlotNotes(
	table []models.Model,
	sel harness.ModelSelection,
) (notes []string, warning string) {
	held, haiku, unslotted := claude.Placement(table, sel)
	for _, h := range held {
		notes = append(notes, fmt.Sprintf("%s takes the %s", safe(h[1]), h[0]))
	}
	if haiku != "" {
		warning = fmt.Sprintf(claudeHaikuWarning, safe(haiku))
	}
	if len(unslotted) > 0 {
		notes = append(notes, claudeNoTierNote+safe(strings.Join(unslotted, ", ")))
	}
	return notes, warning
}

// pickClaudeDefault asks which pick is the default; a single pick is the default without asking.
func pickClaudeDefault(deps Deps, p presenter, picks []string) (string, error) {
	if len(picks) == 1 {
		return picks[0], nil
	}
	opts := make([]option, len(picks))
	for i, alias := range picks {
		opts[i] = option{label: safe(alias)}
	}
	i, err := selectMenuOption(deps, p, "Default model for Claude Code", "", opts, 0, true)
	if err != nil {
		return "", err
	}
	return picks[i], nil
}

// pickClaudeModels lists every granted model by vendor. kept are ticked again, in their order, after Back.
func pickClaudeModels(
	deps Deps,
	p presenter,
	table []models.Model,
	kept []string,
) ([]string, error) {
	var aliases []string
	var flat []models.Model
	for _, g := range models.Grouped(table) {
		for _, m := range g.Models {
			aliases, flat = append(aliases, m.Alias), append(flat, m)
		}
	}
	if len(flat) == 0 {
		return nil, errors.New("no models available to choose")
	}
	if p.inline == nil {
		return pickClaudeModelsPlain(deps, p, table, aliases, kept)
	}
	c := claudeModelChoice(p, table, flat, aliases, kept)
	for {
		indexes, err := p.inline.Select(c)
		if err != nil {
			return nil, err
		}
		if len(indexes) > 0 {
			picks := make([]string, len(indexes))
			for i, idx := range indexes {
				picks[i] = aliases[idx]
			}
			return picks, nil
		}
		p.ui.railWarn(claudeNoneChosen)
	}
}

// claudeModelChoice is the inline model list: tick order kept, and a sixth model from another vendor refused.
func claudeModelChoice(p presenter, table, flat []models.Model, aliases, kept []string) choice {
	others := func(picked []bool) int {
		var on []string
		for j, ok := range picked {
			if ok {
				on = append(on, aliases[j])
			}
		}
		return otherCount(table, on)
	}
	c := choice{
		title: "Choose models for Claude Code",
		multi: true, ordered: true, allowBack: true, rail: true, theme: &p.ui.th,
		status: func(picked []bool) string {
			return fmt.Sprintf(claudeLimitStatus, others(picked), claude.MaxOtherModels)
		},
		refuse: func(picked []bool, i int) string {
			if !claude.IsClaude(table, aliases[i]) && others(picked) >= claude.MaxOtherModels {
				return claudeLimitRefusal
			}
			return ""
		},
	}
	for _, m := range flat {
		c.options = append(c.options, option{
			label: safe(m.Alias), detail: modelDetail(m), picked: slices.Contains(kept, m.Alias),
		})
	}
	for _, a := range kept {
		if i := slices.Index(aliases, a); i >= 0 {
			c.order = append(c.order, i)
		}
	}
	return c
}

// pickClaudeModelsPlain is the numbered form of the model list, read like the Harnesses step.
func pickClaudeModelsPlain(
	deps Deps,
	p presenter,
	table []models.Model,
	offered, kept []string,
) ([]string, error) {
	p.ui.line(2, "Choose models for Claude Code:")
	p.ui.note(2, claudeLimitNote)
	printGroupedModels(p, models.Grouped(table))
	if len(kept) > 0 {
		p.ui.line(2, claudeKeptNote+safe(strings.Join(kept, ", ")))
	}
	for range 3 {
		answer, err := deps.Prompter.ReadLine(claudePlainPrompt)
		if errors.Is(err, io.EOF) || errors.Is(err, errInputEnded) {
			return nil, errInputEnded
		}
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimSpace(answer)
		if strings.EqualFold(trimmed, "b") || strings.EqualFold(trimmed, "back") {
			return nil, errBack
		}
		picks, ok := parseModelPicks(trimmed, offered)
		switch {
		case trimmed == "" && len(kept) > 0:
			return kept, nil
		case !ok:
			p.ui.line(2, "Choose a number or model id from the list.")
		case len(picks) == 0:
			p.ui.line(2, claudeNoneChosen)
		case otherCount(table, picks) > claude.MaxOtherModels:
			p.ui.line(2, claudePlainLimit)
		default:
			return picks, nil
		}
	}
	return nil, errors.New("no answer: expected model numbers or ids")
}

// otherCount counts the aliases from vendors other than Anthropic, which Claude Code's limit applies to.
func otherCount(table []models.Model, aliases []string) int {
	n := 0
	for _, a := range aliases {
		if !claude.IsClaude(table, a) {
			n++
		}
	}
	return n
}

// modelDetail is a model's display name, when it differs from its id, and its vendor.
func modelDetail(m models.Model) string {
	detail := safe(m.Group())
	if name := safe(m.DisplayName()); name != "" && name != safe(m.Alias) {
		detail = name + " · " + detail
	}
	return detail
}

// printGroupedModels numbers the models by vendor, in the order the plain model lists read them.
func printGroupedModels(p presenter, groups []models.Group) {
	n := 0
	for _, g := range groups {
		p.ui.line(4, safe(g.Name))
		for _, m := range g.Models {
			n++
			entry := fmt.Sprintf("%d) %s", n, safe(m.Alias))
			if name := safe(m.DisplayName()); name != "" && name != safe(m.Alias) {
				entry += " (" + name + ")"
			}
			p.ui.line(6, entry)
		}
	}
}
