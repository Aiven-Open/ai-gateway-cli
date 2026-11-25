// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package claude

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

// MaxOtherModels is how many models from vendors other than Anthropic Claude Code holds: its custom
// option and its four tiers.
const MaxOtherModels = 5

// IsClaude reports whether alias is a granted Anthropic model: filed under Anthropic, or with no
// owner and a claude- prefix. An Aiven-owned claude-opus-5-5 is Aiven's model, which Claude Code
// holds in a slot of its own.
func IsClaude(t []models.Model, alias string) bool {
	m, ok := models.Find(t, alias)
	return ok && m.IsClaude()
}

// tierVars are Claude Code's tier slots in the order models from other vendors take them. Haiku
// goes last, on the assumption that Claude Code sends its background calls there.
var tierVars = []string{
	"ANTHROPIC_DEFAULT_FABLE_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
}

// tier is the alias s holds in the tier slot v names.
func (s slots) tier(v string) string {
	switch v {
	case "ANTHROPIC_DEFAULT_FABLE_MODEL":
		return s.fable
	case "ANTHROPIC_DEFAULT_OPUS_MODEL":
		return s.opus
	case "ANTHROPIC_DEFAULT_SONNET_MODEL":
		return s.sonnet
	case "ANTHROPIC_DEFAULT_HAIKU_MODEL":
		return s.haiku
	}
	return ""
}

// withTier is s with alias in the tier slot v names.
func (s slots) withTier(v, alias string) slots {
	switch v {
	case "ANTHROPIC_DEFAULT_FABLE_MODEL":
		s.fable = alias
	case "ANTHROPIC_DEFAULT_OPUS_MODEL":
		s.opus = alias
	case "ANTHROPIC_DEFAULT_SONNET_MODEL":
		s.sonnet = alias
	case "ANTHROPIC_DEFAULT_HAIKU_MODEL":
		s.haiku = alias
	}
	return s
}

// fillSlots places a Choose models answer. Each ticked Claude model takes the tier its alias names.
// The first model from another vendor takes the custom option; the rest take the tiers in
// fillOrder. A tier no pick takes keeps its Claude model, else the defaultSlots tier when Anthropic
// models are granted, else the default. The tiers come from the catalogue alone, never from pins
// the settings already hold, so the same picks always give the same slots.
func fillSlots(t []models.Model, sel harness.ModelSelection) (slots, error) {
	main := bare(cmp.Or(sel.Model, sel.Models[0]))
	others := otherPicks(t, sel.Models)
	if len(others) > MaxOtherModels {
		return slots{}, fmt.Errorf("%d models from other vendors chosen; Claude Code holds at most %d",
			len(others), MaxOtherModels)
	}
	held, ticked := withClaudePicks(t, claudeTiers(t), sel.Models)
	spare := slots{fable: main, opus: main, sonnet: main, haiku: main}
	if _, ok := models.FindPreset(t, models.PresetAnthropic); ok {
		spare = defaultSlots(t)
	}
	s := slots{main: main}
	queue := others
	if len(queue) > 0 {
		s.custom, queue = queue[0], queue[1:]
	}
	for _, v := range fillOrder(held, ticked) {
		alias := cmp.Or(held.tier(v), spare.tier(v))
		if len(queue) > 0 {
			alias, queue = queue[0], queue[1:]
		}
		s = s.withTier(v, alias)
	}
	return s, nil
}

// otherPicks are the picks that are not Anthropic models, once each, in the order chosen.
func otherPicks(t []models.Model, picks []string) []string {
	var out []string
	for _, p := range picks {
		if alias := bare(p); !IsClaude(t, alias) && !slices.Contains(out, alias) {
			out = append(out, alias)
		}
	}
	return out
}

// claudeTiers holds the granted Anthropic model each tier's own order names; empty where the key
// grants none.
func claudeTiers(t []models.Model) slots {
	own := models.AnthropicTiers(t)
	return slots{fable: own.Fable, opus: own.Opus, sonnet: own.Sonnet, haiku: own.Haiku}
}

// namedTier is the tier slot an alias names, checked in tierVars order (claude-fable-5-2 names
// ANTHROPIC_DEFAULT_FABLE_MODEL), or "" when it names none.
func namedTier(alias string) string {
	for _, v := range tierVars {
		word := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(v, "ANTHROPIC_DEFAULT_"), "_MODEL"))
		if strings.Contains(alias, word) {
			return v
		}
	}
	return ""
}

// withClaudePicks is held with each ticked Anthropic model in the tier its alias names, ahead of
// the tier's preset model, and the tiers they took. The first pick naming a tier takes it; a later
// one, or one naming no tier, gets no slot by name and stays reachable through gateway model
// discovery, though it may remain where a tier's preset model already put it.
func withClaudePicks(t []models.Model, held slots, picks []string) (slots, []string) {
	var claimed []string
	for _, p := range picks {
		alias := bare(p)
		v := namedTier(alias)
		if v == "" || !IsClaude(t, alias) || slices.Contains(claimed, v) {
			continue
		}
		claimed = append(claimed, v)
		held = held.withTier(v, alias)
	}
	return held, claimed
}

// fillOrder lists the tiers models from other vendors take after the custom option: tiers with no
// Claude model; then fable, opus and sonnet holding a Claude model nobody ticked; then those
// holding a ticked one; then haiku, ticked or not. Each group keeps tierVars order.
func fillOrder(held slots, ticked []string) []string {
	rank := func(v string) int {
		switch {
		case held.tier(v) == "":
			return 0
		case v == "ANTHROPIC_DEFAULT_HAIKU_MODEL":
			return 3
		case slices.Contains(ticked, v):
			return 2
		}
		return 1
	}
	return slices.SortedStableFunc(slices.Values(tierVars), func(a, b string) int {
		return cmp.Compare(rank(a), rank(b))
	})
}

// keptSlots is defaultSlots with each granted model the settings already pin in a slot, so Keep current and --yes
// leave a chosen mix of models where it is.
func keptSlots(t []models.Model, env map[string]any) slots {
	s := defaultSlots(t)
	for _, v := range tierVars {
		s = s.withTier(v, grantedPin(t, env, v, s.tier(v)))
	}
	s.custom = grantedPin(t, env, "ANTHROPIC_CUSTOM_MODEL_OPTION", "")
	return s
}

// grantedPin is the granted alias env pins under key, without its [1m] suffix, else fallback.
func grantedPin(t []models.Model, env map[string]any, key, fallback string) string {
	if v, ok := env[key].(string); ok {
		if _, granted := models.Find(t, bare(v)); granted {
			return bare(v)
		}
	}
	return fallback
}
