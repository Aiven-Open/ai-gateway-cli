// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package models_test

import (
	"slices"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

func TestPresetsFilterAndResolveByGrantedCatalogue(t *testing.T) {
	t.Parallel()

	t.Run("full catalogue offers all four presets in order", func(t *testing.T) {
		t.Parallel()
		catalogue := []models.Model{
			{Alias: models.Router},
			{Alias: "claude-haiku-5.5"},
			{Alias: "claude-opus-5-5"},
			{Alias: "claude-sonnet-5-5"},
			{Alias: "deepseek-v3.2"},
			{Alias: "deepseek-v4.1-flash"},
			{Alias: "gemini-3.8-flash"},
			{Alias: "gemini-4-argon"},
			{Alias: "glm-4.7-flash"},
			{Alias: "glm-5"},
			{Alias: "gpt-5.4"},
			{Alias: "gpt-6.1-sol"},
			{Alias: "gpt-oss-120b"},
		}
		presets := models.Presets(catalogue)
		var ids []models.PresetID
		for _, p := range presets {
			ids = append(ids, p.ID)
		}
		wantIDs := []models.PresetID{
			models.PresetAnthropic,
			models.PresetOpenAI,
			models.PresetGemini,
			models.PresetOpenWeight,
		}
		if !slices.Equal(ids, wantIDs) {
			t.Fatalf("preset ids = %v, want %v", ids, wantIDs)
		}

		anth := presets[0]
		if anth.Main != "claude-sonnet-5-5" || anth.Opus != "claude-opus-5-5" ||
			anth.Sonnet != "claude-sonnet-5-5" || anth.Haiku != "claude-haiku-5.5" ||
			anth.Fable != "claude-opus-5-5" {
			t.Errorf("Anthropic preset = %+v", anth)
		}

		oai := presets[1]
		if oai.Main != "gpt-6.1-sol" || oai.Opus != "gpt-6.1-sol" || oai.Haiku != "gpt-6.1-sol" {
			t.Errorf("OpenAI preset = %+v", oai)
		}

		gem := presets[2]
		if gem.Main != "gemini-4-argon" || gem.Opus != "gemini-4-argon" ||
			gem.Sonnet != "gemini-4-argon" || gem.Haiku != "gemini-3.8-flash" {
			t.Errorf("Gemini preset = %+v", gem)
		}

		ow := presets[3]
		if ow.Main != "deepseek-v4.1-flash" || ow.Opus != "deepseek-v4.1-flash" ||
			ow.Sonnet != "deepseek-v4.1-flash" || ow.Haiku != "deepseek-v4.1-flash" ||
			ow.Fable != "deepseek-v4.1-flash" || ow.Custom != "deepseek-v4.1-flash" {
			t.Errorf("Open-weight preset = %+v", ow)
		}
	})

	t.Run("Gemini-only key resolves Gemini preset and SonnetOrFirst", func(t *testing.T) {
		t.Parallel()
		gemOnly := []models.Model{
			{Alias: "gemini-3.5-flash-lite"},
			{Alias: "gemini-3.8-flash"},
			{Alias: "gemini-4-argon"},
		}
		presets := models.Presets(gemOnly)
		if len(presets) != 1 || presets[0].ID != models.PresetGemini ||
			presets[0].Main != "gemini-4-argon" || presets[0].Haiku != "gemini-3.8-flash" {
			t.Fatalf("Gemini-only presets = %+v", presets)
		}
		if got := models.SonnetOrFirst(gemOnly); got != "gemini-4-argon" {
			t.Errorf("SonnetOrFirst(Gemini-only) = %q, want gemini-4-argon", got)
		}
	})

	t.Run("open-weight-only LA key prefers latest DeepSeek over GLM", func(t *testing.T) {
		t.Parallel()
		la := []models.Model{
			{Alias: "deepseek-v3.2"},
			{Alias: "devstral-2-123b"},
			{Alias: "glm-4.7-flash"},
			{Alias: "glm-5"},
			{Alias: "gpt-oss-120b"},
			{Alias: "qwen3-coder-480b"},
		}
		presets := models.Presets(la)
		if len(presets) != 1 || presets[0].ID != models.PresetOpenWeight {
			t.Fatalf("presets = %+v, want only Open-weight", presets)
		}
		if presets[0].Main != "deepseek-v3.2" || presets[0].Haiku != "glm-4.7-flash" {
			t.Errorf("Open-weight preset = %+v", presets[0])
		}
		if got := models.SonnetOrFirst(la); got != "deepseek-v3.2" {
			t.Errorf("SonnetOrFirst(LA) = %q, want deepseek-v3.2", got)
		}
		if got := models.CodexOrFirst(la); got != "deepseek-v3.2" {
			t.Errorf("CodexOrFirst(LA) = %q, want deepseek-v3.2", got)
		}
	})

	t.Run("OpenAI preset excludes gpt-oss and falls back to sorted gpt-*", func(t *testing.T) {
		t.Parallel()
		if presets := models.Presets(
			[]models.Model{{Alias: "gpt-oss-120b"}},
		); len(presets) != 1 ||
			presets[0].ID != models.PresetOpenWeight {
			t.Fatalf("gpt-oss-120b alone gave presets %+v, want only Open-weight", presets)
		}
		p, ok := models.FindPreset(
			[]models.Model{{Alias: "gpt-oss-120b"}, {Alias: "gpt-5.4"}},
			models.PresetOpenAI,
		)
		if !ok || p.Main != "gpt-5.4" {
			t.Fatalf("FindPreset(OpenAI) = %+v, %v; want gpt-5.4", p, ok)
		}
		p, ok = models.FindPreset(
			[]models.Model{{Alias: "gpt-oss-120b"}, {Alias: "gpt-custom-mini"}},
			models.PresetOpenAI,
		)
		if !ok || p.Main != "gpt-custom-mini" {
			t.Fatalf("FindPreset(OpenAI fallback) = %+v, %v; want gpt-custom-mini", p, ok)
		}
	})

	t.Run("Anthropic preset falls back within tier when sonnet absent", func(t *testing.T) {
		t.Parallel()
		p, ok := models.FindPreset([]models.Model{{Alias: "claude-opus-4-7"}}, models.PresetAnthropic)
		if !ok || p.Main != "claude-opus-4-7" || p.Sonnet != "claude-opus-4-7" ||
			p.Haiku != "claude-opus-4-7" {
			t.Fatalf("Anthropic opus-only = %+v, %v", p, ok)
		}
	})

	t.Run(
		"Open-weight preset falls back Haiku slot to Main when no fast model granted",
		func(t *testing.T) {
			t.Parallel()
			p, ok := models.FindPreset(
				[]models.Model{{Alias: "qwen3-coder-480b"}, {Alias: "deepseek-v3.2"}},
				models.PresetOpenWeight,
			)
			if !ok || p.Main != "deepseek-v3.2" || p.Haiku != "deepseek-v3.2" {
				t.Fatalf("Open-weight = %+v, %v", p, ok)
			}
		},
	)

	t.Run(
		"fast-only open-weight catalogue omits Open-weight preset without primary model",
		func(t *testing.T) {
			t.Parallel()
			if presets := models.Presets([]models.Model{{Alias: "glm-4.7-flash"}}); len(presets) != 0 {
				t.Fatalf("presets = %+v, want none without a primary model", presets)
			}
			if got := models.OpenWeightOrFirst(
				[]models.Model{{Alias: "a-other"}, {Alias: "glm-4.7-flash"}},
			); got != "glm-4.7-flash" {
				t.Errorf("OpenWeightOrFirst = %q, want glm-4.7-flash", got)
			}
		},
	)
}
