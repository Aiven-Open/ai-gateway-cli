// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package models_test

import (
	"slices"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

func TestGroupTrustsTheCatalogueOwnerThenReviewedIDsAndNeverAPrefix(t *testing.T) {
	for _, tc := range []struct {
		m           models.Model
		group, name string
	}{
		{models.Model{Alias: models.Router, Owner: "Aiven"}, models.RoutingGroup, models.Router},
		// LiteLLM's relayed listing calls every model OpenAI's; the reviewed id wins.
		{models.Model{Alias: "claude-sonnet-5-5", Owner: "openai"}, "Anthropic", "Claude Sonnet 5.5"},
		{models.Model{Alias: "glm-5"}, "Z.ai", "GLM 5"},
		// A private alias that reuses a public id belongs to its owner and keeps its id.
		{models.Model{Alias: "claude-opus-5-5", Owner: "Aiven"}, "Aiven", "claude-opus-5-5"},
		{models.Model{Alias: "claude-opus-5-5", Owner: "anthropic"}, "Anthropic", "Claude Opus 5.5"},
		{models.Model{Alias: "gpt-oss-20b", Owner: "openai"}, "OpenAI", "gpt-oss 20B"},
		{models.Model{Alias: "claude-next", Owner: "openai"}, models.UnknownGroup, "claude-next"},
		{models.Model{Alias: "claude-next"}, models.UnknownGroup, "claude-next"},
		{models.Model{Alias: "gpt-next", Owner: "OpenAI"}, "OpenAI", "gpt-next"},
		{models.Model{Alias: "private-customer-model", Owner: "Aiven"}, "Aiven", "private-customer-model"},
		{models.Model{Alias: "qwen-x", Owner: "Alibaba Cloud"}, "Alibaba Cloud", "qwen-x"},
		// The catalogue files Qwen under its vendor; the reviewed name still applies.
		{models.Model{Alias: "qwen3-32b", Owner: "Alibaba"}, "Alibaba", "Qwen3 32B"},
		{models.Model{Alias: "m", Owner: "zai", Name: "Catalogue Name"}, "Z.ai", "Catalogue Name"},
		{models.Model{Alias: "glm-5", Name: "GLM-5 (catalogue)"}, "Z.ai", "GLM-5 (catalogue)"},
	} {
		if g, n := tc.m.Group(), tc.m.DisplayName(); g != tc.group || n != tc.name {
			t.Errorf("%+v: group %q, name %q; want %q, %q", tc.m, g, n, tc.group, tc.name)
		}
	}
}

func TestGroupedPutsPublishersFirstThenRoutingThenUnknown(t *testing.T) {
	got := models.Grouped([]models.Model{
		{Alias: "mystery"}, {Alias: models.Router}, {Alias: "gpt-5.4"}, {Alias: "claude-opus-5-5"}, {Alias: "claude-haiku-4-5"},
	})
	var names []string
	for _, g := range got {
		names = append(names, g.Name)
	}
	if want := []string{"Anthropic", "OpenAI", models.RoutingGroup, models.UnknownGroup}; !slices.Equal(names, want) {
		t.Fatalf("groups %q, want %q", names, want)
	}
	if a := got[0].Models; len(a) != 2 || a[0].Alias != "claude-haiku-4-5" || a[1].Alias != "claude-opus-5-5" {
		t.Errorf("Anthropic models %v, want sorted by id", a)
	}
}

// A listing that relays LiteLLM's placeholder owner still groups every reviewed public id; an id that is not
// reviewed, such as a private alias, stays unknown rather than borrowing a publisher.
func TestEveryReviewedIDHasAPublisherUnderThePlaceholderOwner(t *testing.T) {
	ids := []string{
		"claude-haiku-4-5", "claude-haiku-5-5", "claude-opus-4-5", "claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8",
		"claude-opus-5", "claude-opus-5-5", "claude-sonnet-4-5", "claude-sonnet-4-6", "claude-sonnet-5",
		"claude-sonnet-5-5", "deepseek-v3.1", "deepseek-v3.2", "devstral-2-123b", "private-customer-model",
		"gemini-3.5-flash-lite", "gemini-3.8-flash", "gemini-4-argon", "gemma-3-12b", "gemma-3-27b", "gemma-3-4b",
		"glm-4.7", "glm-4.7-flash", "glm-5", "gpt-5.4", "gpt-5.5", "gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra",
		"gpt-6-astra", "gpt-6-luna", "gpt-6-sol", "gpt-6.1-sol", "gpt-oss-120b", "gpt-oss-20b", "gpt-oss-safeguard-120b",
		"gpt-oss-safeguard-20b", "grok-4.6", "grok-4.7", "kimi-k2.5", "kimi-k3", "magistral-small", "minimax-m2",
		"minimax-m2.1", "minimax-m2.5", "ministral-3-14b", "ministral-3-3b", "ministral-3-8b", "mistral-7b",
		"mistral-large-2402", "mistral-pixtral-large", "mixtral-8x7b", "nemotron-nano-12b-v2-vl", "nemotron-nano-3-30b",
		"nemotron-nano-9b-v2", "nemotron-super-3-120b", "nova-2-lite", "nova-lite", "nova-micro", "nova-pro", "qwen3-235b",
		"qwen3-32b", "qwen3-coder-30b", "qwen3-coder-480b", "qwen3-next-80b", "qwen3-vl-235b", "voxtral-mini-3b",
		"voxtral-small-24b",
	}
	for _, id := range ids {
		m := models.Model{Alias: id, Owner: "openai"}
		g := m.Group()
		if id == "private-customer-model" {
			if g != models.UnknownGroup {
				t.Errorf("%s grouped under %q, want unknown", id, g)
			}
			continue
		}
		if g == models.UnknownGroup || g == "" {
			t.Errorf("%s has no reviewed publisher", id)
		}
	}
}
