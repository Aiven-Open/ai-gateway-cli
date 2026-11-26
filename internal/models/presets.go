// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package models

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// PresetID identifies a curated v1 model preset.
type PresetID string

const (
	PresetAnthropic  PresetID = "anthropic"
	PresetOpenAI     PresetID = "openai"
	PresetGemini     PresetID = "gemini"
	PresetOpenWeight PresetID = "open-weight"
)

// Preset is a curated model bundle resolved against a key's granted catalogue.
type Preset struct {
	ID     PresetID
	Name   string
	Main   string
	Opus   string
	Sonnet string
	Haiku  string
	Fable  string
}

var (
	anthropicSonnetOrder = []string{
		"claude-sonnet-5-5", "claude-sonnet-5", "claude-sonnet-4-6", "claude-sonnet-4-5", "claude-sonnet",
	}
	anthropicOpusOrder = []string{
		"claude-opus-5-5", "claude-opus-5", "claude-opus-4-8", "claude-opus-4-7",
		"claude-opus-4-6", "claude-opus-4-5", "claude-opus",
	}
	anthropicHaikuOrder = []string{
		"claude-haiku-5-5", "claude-haiku-5.5", "claude-haiku-4-5", "claude-haiku",
	}
	openAIOrder = []string{
		"gpt-6.1-sol", "gpt-6-sol", "gpt-5.4", "gpt-5.5",
	}
	geminiMainOrder = []string{
		"gemini-4-neon", "gemini-4-carbon", "gemini-4-argon",
		"gemini-3.8-pro", "gemini-3.5-pro", "gemini-2.5-pro",
	}
	geminiFastOrder = []string{
		"gemini-4-hydrogen", "gemini-3.8-flash", "gemini-3.5-flash",
		"gemini-3.5-flash-lite", "gemini-2.5-flash",
	}
	openWeightMainOrder = []string{
		"deepseek-v4.1", "deepseek-v4", "deepseek-v4.1-flash", "deepseek-v3.5", "deepseek-v3.2",
		"glm-5.3", "glm-5", "qwen3-coder-480b", "gpt-oss-120b", "kimi-k3",
		"minimax-m2.5", "glm-4.7", "deepseek-v3.1", "kimi-k2.5", "qwen3-235b", "devstral-2-123b",
	}
	openWeightFastOrder = []string{
		"deepseek-v4.1-flash", "glm-4.7-flash", "qwen3-coder-30b", "gpt-oss-20b",
		"gemma-3-27b", "nemotron-nano-3-30b",
	}
)

// Presets returns the curated v1 presets that have at least one granted primary model in ms,
// ordered Anthropic, OpenAI, Gemini, Open-weight.
func Presets(ms []Model) []Preset {
	var out []Preset
	for _, id := range []PresetID{PresetAnthropic, PresetOpenAI, PresetGemini, PresetOpenWeight} {
		if p, ok := FindPreset(ms, id); ok {
			out = append(out, p)
		}
	}
	return out
}

// FindPreset resolves id against ms, returning false when ms grants none of that preset's primary models.
func FindPreset(ms []Model, id PresetID) (Preset, bool) {
	switch id {
	case PresetAnthropic:
		own := claudeModels(ms)
		// A granted Claude model outside the curated orders still keeps Claude Code on Claude.
		return anthropicPreset(own, FirstAlias(own))
	case PresetOpenAI:
		main := openAIMain(ms)
		if main == "" {
			return Preset{}, false
		}
		return Preset{
			ID:     PresetOpenAI,
			Name:   "OpenAI",
			Main:   main,
			Opus:   main,
			Sonnet: main,
			Haiku:  main,
			Fable:  main,
		}, true
	case PresetGemini:
		main := geminiMain(ms)
		if main == "" {
			return Preset{}, false
		}
		haiku := cmp.Or(FirstGranted(ms, geminiFastOrder...), main)
		return Preset{
			ID:     PresetGemini,
			Name:   "Gemini",
			Main:   main,
			Opus:   main,
			Sonnet: main,
			Haiku:  haiku,
			Fable:  main,
		}, true
	case PresetOpenWeight:
		main := FirstGranted(ms, openWeightMainOrder...)
		if main == "" {
			return Preset{}, false
		}
		haiku := cmp.Or(FirstGranted(ms, openWeightFastOrder...), main)
		return Preset{
			ID:     PresetOpenWeight,
			Name:   "Open-weight",
			Main:   main,
			Opus:   main,
			Sonnet: main,
			Haiku:  haiku,
			Fable:  main,
		}, true
	}
	return Preset{}, false
}

func openAIMain(ms []Model) string {
	if a := FirstGranted(ms, openAIOrder...); a != "" {
		return a
	}
	for _, m := range Sorted(ms) {
		if strings.HasPrefix(m.Alias, "gpt-") && m.Alias != "gpt-oss" &&
			!strings.HasPrefix(m.Alias, "gpt-oss-") {
			return m.Alias
		}
	}
	return ""
}

func geminiMain(ms []Model) string {
	if a := FirstGranted(ms, geminiMainOrder...); a != "" {
		return a
	}
	for _, m := range Sorted(ms) {
		if strings.HasPrefix(m.Alias, "gemini-") && !strings.Contains(m.Alias, "flash") {
			return m.Alias
		}
	}
	if a := FirstGranted(ms, geminiFastOrder...); a != "" {
		return a
	}
	for _, m := range Sorted(ms) {
		if strings.HasPrefix(m.Alias, "gemini-") {
			return m.Alias
		}
	}
	return ""
}

// OpenWeightOrFirst returns the first granted Open-weight model in preference order, else FirstAlias.
func OpenWeightOrFirst(ms []Model) string {
	if a := FirstGranted(ms, openWeightMainOrder...); a != "" {
		return a
	}
	if a := FirstGranted(ms, openWeightFastOrder...); a != "" {
		return a
	}
	return FirstAlias(ms)
}

// anthropicPreset is the Anthropic preset from the curated orders over ms, with fallback as the main model when
// none of them is granted; false when there is no main model.
func anthropicPreset(ms []Model, fallback string) (Preset, bool) {
	sonnet := FirstGranted(ms, anthropicSonnetOrder...)
	opus := FirstGranted(ms, anthropicOpusOrder...)
	haiku := FirstGranted(ms, anthropicHaikuOrder...)
	main := cmp.Or(sonnet, opus, haiku, fallback)
	if main == "" {
		return Preset{}, false
	}
	opus = cmp.Or(opus, main)
	sonnet = cmp.Or(sonnet, main)
	haiku = cmp.Or(haiku, main)
	fable := cmp.Or(FirstGranted(ms, "claude-opus-5-5", "claude-fable"), opus, main)
	return Preset{
		ID:     PresetAnthropic,
		Name:   "Anthropic",
		Main:   main,
		Opus:   opus,
		Sonnet: sonnet,
		Haiku:  haiku,
		Fable:  fable,
	}, true
}

// CuratedAnthropic is the Anthropic preset from the curated orders alone, whoever owns the models, for fills that
// drop a model another vendor owns themselves. A model outside the orders never becomes its main.
func CuratedAnthropic(ms []Model) (Preset, bool) { return anthropicPreset(ms, "") }

// IsClaude reports whether m is an Anthropic model: the catalogue files it under Anthropic, or its publisher is missing
// or not one the CLI has reviewed and the alias starts with claude-. A model a known vendor owns, such as an
// Aiven-owned claude-opus-5-5, is not.
func (m Model) IsClaude() bool {
	switch g := m.Group(); {
	case g == "Anthropic":
		return true
	case g == UnknownGroup, g != RoutingGroup && !knownPublisher(g):
		return strings.HasPrefix(m.Alias, "claude-")
	}
	return false
}

// claudeModels is ms without the models IsClaude rejects.
func claudeModels(ms []Model) []Model {
	return slices.DeleteFunc(slices.Clone(ms), func(m Model) bool { return !m.IsClaude() })
}

// AnthropicTiers returns the first granted Anthropic model of each Claude tier's own order. A model
// the catalogue files under another vendor (an Aiven-owned claude-opus-5-5) is no Anthropic model.
// A tier the key grants none of stays empty, where FindPreset would fill it from another tier. The
// fable tier takes the highest-versioned granted claude-fable model.
func AnthropicTiers(ms []Model) Preset {
	own := claudeModels(ms)
	var fables []string
	for _, m := range own {
		if strings.Contains(m.Alias, "claude-fable") {
			fables = append(fables, m.Alias)
		}
	}
	return Preset{
		ID:     PresetAnthropic,
		Name:   "Anthropic",
		Opus:   FirstGranted(own, anthropicOpusOrder...),
		Sonnet: FirstGranted(own, anthropicSonnetOrder...),
		Haiku:  FirstGranted(own, anthropicHaikuOrder...),
		Fable: cmp.Or(
			slices.MaxFunc(append([]string{""}, fables...), compareVersions),
			FirstGranted(own, "claude-opus-5-5"),
		),
	}
}

// compareVersions orders aliases by the numbers in them, part by part, so 5-10 ranks above 5-9.
func compareVersions(a, b string) int {
	isNotDigit := func(r rune) bool { return r < '0' || r > '9' }
	pa, pb := strings.FieldsFunc(a, isNotDigit), strings.FieldsFunc(b, isNotDigit)
	for i := 0; i < min(len(pa), len(pb)); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if c := cmp.Compare(x, y); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(pa), len(pb))
}
