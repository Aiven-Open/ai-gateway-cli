// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package models

import (
	"cmp"
	"slices"
	"strings"
)

// PresetID identifies a curated v1 model preset.
type PresetID string

const (
	PresetAnthropic  PresetID = "anthropic"
	PresetOpenAI     PresetID = "openai"
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
	Custom string
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
	openWeightMainOrder = []string{
		"glm-5", "qwen3-coder-480b", "gpt-oss-120b", "deepseek-v3.2", "kimi-k3",
		"minimax-m2.5", "glm-4.7", "deepseek-v3.1", "kimi-k2.5", "qwen3-235b", "devstral-2-123b",
	}
	openWeightFastOrder = []string{
		"glm-4.7-flash", "qwen3-coder-30b", "gpt-oss-20b", "gemma-3-27b", "nemotron-nano-3-30b",
	}
)

// Presets returns the curated v1 presets that have at least one granted primary model in ms,
// ordered Anthropic, OpenAI, Open-weight.
func Presets(ms []Model) []Preset {
	var out []Preset
	for _, id := range []PresetID{PresetAnthropic, PresetOpenAI, PresetOpenWeight} {
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
		sonnet := FirstGranted(ms, anthropicSonnetOrder...)
		opus := FirstGranted(ms, anthropicOpusOrder...)
		haiku := FirstGranted(ms, anthropicHaikuOrder...)
		main := cmp.Or(sonnet, opus, haiku)
		if main == "" {
			return Preset{}, false
		}
		opus = cmp.Or(opus, main)
		sonnet = cmp.Or(sonnet, main)
		haiku = cmp.Or(haiku, main)
		fable := cmp.Or(FirstGranted(ms, "claude-opus-5-5", "claude-fable"), opus, main)
		custom := cmp.Or(firstAnthropicOther(ms, opus, sonnet, haiku, fable), main)
		return Preset{
			ID:     PresetAnthropic,
			Name:   "Anthropic",
			Main:   main,
			Opus:   opus,
			Sonnet: sonnet,
			Haiku:  haiku,
			Fable:  fable,
			Custom: custom,
		}, true
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
			Custom: main,
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
			Custom: main,
		}, true
	}
	return Preset{}, false
}

func firstAnthropicOther(ms []Model, used ...string) string {
	for _, list := range [][]string{anthropicSonnetOrder, anthropicOpusOrder, anthropicHaikuOrder} {
		for _, alias := range list {
			if _, ok := Find(ms, alias); ok && !slices.Contains(used, alias) {
				return alias
			}
		}
	}
	return ""
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
