// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package models_test

import (
	"slices"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

func TestPresetsFilterAndResolveByGrantedCatalogue(t *testing.T) {
	t.Parallel()

	t.Run("full catalogue offers all three presets in order", func(t *testing.T) {
		t.Parallel()
		catalogue := []models.Model{
			{Alias: models.Router},
			{Alias: "claude-haiku-5.5"},
			{Alias: "claude-opus-5-5"},
			{Alias: "claude-sonnet-5-5"},
			{Alias: "deepseek-v3.2"},
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
		wantIDs := []models.PresetID{models.PresetAnthropic, models.PresetOpenAI, models.PresetOpenWeight}
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

		ow := presets[2]
		if ow.Main != "glm-5" || ow.Opus != "glm-5" || ow.Sonnet != "glm-5" ||
			ow.Haiku != "glm-4.7-flash" || ow.Fable != "glm-5" || ow.Custom != "glm-5" {
			t.Errorf("Open-weight preset = %+v", ow)
		}
	})

	t.Run("open-weight-only LA key offers only Open-weight preset", func(t *testing.T) {
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
		if presets[0].Main != "glm-5" || presets[0].Haiku != "glm-4.7-flash" {
			t.Errorf("Open-weight preset = %+v", presets[0])
		}
		if got := models.SonnetOrFirst(la); got != "glm-5" {
			t.Errorf("SonnetOrFirst(LA) = %q, want glm-5", got)
		}
		if got := models.CodexOrFirst(la); got != "glm-5" {
			t.Errorf("CodexOrFirst(LA) = %q, want glm-5", got)
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
			if !ok || p.Main != "qwen3-coder-480b" || p.Haiku != "qwen3-coder-480b" {
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
