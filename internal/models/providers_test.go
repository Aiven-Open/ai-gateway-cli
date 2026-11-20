// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package models_test

import (
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

func TestProviderAliasDerivesGrantedMatches(t *testing.T) {
	t.Parallel()

	granted := []models.Model{
		{Alias: "claude-sonnet-4-5", Owner: "Anthropic"},
		{Alias: "claude-sonnet-5-5", Owner: "Anthropic"},
		{Alias: "claude-opus-4-6", Owner: "Anthropic"},
		{Alias: "claude-haiku-5.5", Owner: "Anthropic"},
		{Alias: "claude-3-5-sonnet", Owner: "Anthropic"},
		{Alias: "gpt-5.4", Owner: "OpenAI"},
		{Alias: "gpt-oss-120b", Owner: "OpenAI"},
		{Alias: "glm-5", Owner: "Z.ai"},
		{Alias: "deepseek-v3.2", Owner: "DeepSeek"},
		{Alias: "qwen3-coder-480b", Owner: "Alibaba"},
		{Alias: "devstral-2-123b", Owner: "Mistral AI"},
		{Alias: "mistral-large-2402", Owner: "Mistral AI"},
		{Alias: "nemotron-nano-9b-v2", Owner: "NVIDIA"},
		{Alias: "nova-pro", Owner: "AWS"},
		{Alias: models.Router, Owner: "Aiven"},
	}

	tests := []struct {
		name string
		id   string
		want string
	}{
		{name: "anthropic dated", id: "claude-sonnet-4-5-20250929", want: "claude-sonnet-4-5"},
		{name: "anthropic vertex dated", id: "claude-sonnet-4-5@20250929", want: "claude-sonnet-4-5"},
		{name: "anthropic vertex default", id: "claude-sonnet-4-5@default", want: "claude-sonnet-4-5"},
		{
			name: "anthropic bedrock dated revision",
			id:   "eu.anthropic.claude-sonnet-4-5-20250929-v1:0",
			want: "claude-sonnet-4-5",
		},
		{
			name: "anthropic bedrock unscoped",
			id:   "anthropic.claude-sonnet-4-5-20250929-v1:0",
			want: "claude-sonnet-4-5",
		},
		{
			name: "anthropic bedrock v1",
			id:   "global.anthropic.claude-opus-4-6-v1",
			want: "claude-opus-4-6",
		},
		{
			name: "anthropic legacy bedrock",
			id:   "us.anthropic.claude-3-5-sonnet-20241022-v2:0",
			want: "claude-3-5-sonnet",
		},
		{
			name: "dotted claude normalized to dashed",
			id:   "eu.anthropic.claude-sonnet-5.5",
			want: "claude-sonnet-5-5",
		},
		{
			name: "dotted claude dated normalized to dashed",
			id:   "claude-sonnet-5.5-20260101",
			want: "claude-sonnet-5-5",
		},
		{
			name: "dotted claude exact match when granted",
			id:   "eu.anthropic.claude-haiku-5.5",
			want: "claude-haiku-5.5",
		},
		{
			name: "dashed claude normalized to dotted when only dotted granted",
			id:   "eu.anthropic.claude-haiku-5-5",
			want: "claude-haiku-5.5",
		},
		{name: "openai dated", id: "gpt-5.4-2026-03-05", want: "gpt-5.4"},
		{name: "openai bedrock unscoped", id: "openai.gpt-oss-120b-1:0", want: "gpt-oss-120b"},
		{name: "openai bedrock global", id: "global.openai.gpt-oss-120b-1:0", want: "gpt-oss-120b"},
		{name: "openai bedrock plain", id: "us.openai.gpt-5.4", want: "gpt-5.4"},
		{name: "zai bedrock", id: "global.zai.glm-5", want: "glm-5"},
		{name: "zhipu bedrock", id: "zhipu.glm-5-v1:0", want: "glm-5"},
		{name: "z-ai bedrock", id: "z-ai.glm-5", want: "glm-5"},
		{name: "deepseek bedrock", id: "us.deepseek.deepseek-v3.2-v1:0", want: "deepseek-v3.2"},
		{name: "deepseek vertex", id: "deepseek-v3.2@20251201", want: "deepseek-v3.2"},
		{name: "qwen bedrock", id: "qwen.qwen3-coder-480b-v1:0", want: "qwen3-coder-480b"},
		{name: "alibaba bedrock", id: "apac.alibaba.qwen3-coder-480b", want: "qwen3-coder-480b"},
		{name: "mistral bedrock", id: "mistral.devstral-2-123b", want: "devstral-2-123b"},
		{
			name: "mistral numeric model keeps version",
			id:   "mistral.mistral-large-2402-v1:0",
			want: "mistral-large-2402",
		},
		{
			name: "nvidia keeps -v2 in alias",
			id:   "nvidia.nemotron-nano-9b-v2",
			want: "nemotron-nano-9b-v2",
		},
		{
			name: "nvidia strips -v1:0 but keeps -v2",
			id:   "nvidia.nemotron-nano-9b-v2-v1:0",
			want: "nemotron-nano-9b-v2",
		},
		{name: "amazon bedrock", id: "amazon.nova-pro-v1:0", want: "nova-pro"},
		{name: "bare granted alias not wrapped", id: "claude-sonnet-5-5", want: ""},
		{name: "ungranted claude", id: "eu.anthropic.claude-opus-4-5-20251101-v1:0", want: ""},
		{name: "unknown scope", id: "moon.anthropic.claude-sonnet-5-5", want: ""},
		{name: "router never matched", id: "aiven-model@default", want: ""},
		{name: "empty", id: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := models.ProviderAlias(granted, tc.id); got != tc.want {
				t.Errorf("ProviderAlias(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestProviderAliasRejectsPublisherMismatches(t *testing.T) {
	t.Parallel()

	// Private alias reusing a public id belongs to Aiven, not Anthropic; LiteLLM placeholder still resolves
	// public models to their reviewed publisher so wrong-vendor prefixes are rejected.
	granted := []models.Model{
		{Alias: "claude-opus-5-5", Owner: "Aiven"},
		{Alias: "claude-sonnet-5-5", Owner: "openai"},
		{Alias: "glm-5", Owner: "Aiven"},
		{Alias: "deepseek-v3.2", Owner: "Aiven"},
		{Alias: "private-only", Owner: "Aiven"},
		{Alias: "custom-qwen", Owner: "Alibaba"},
		{Alias: "unowned-private", Owner: ""},
	}

	tests := []struct {
		id   string
		want string
	}{
		{id: "eu.anthropic.claude-opus-5.5", want: ""},
		{id: "claude-opus-5-5@default", want: ""},
		{id: "openai.claude-sonnet-5-5", want: ""},
		{id: "eu.anthropic.claude-sonnet-5.5", want: "claude-sonnet-5-5"},
		{id: "global.zai.glm-5", want: ""},
		{id: "glm-5@default", want: ""},
		{id: "deepseek-v3.2@20251201", want: ""},
		{id: "private-only@default", want: ""},
		{id: "qwen.custom-qwen-v1:0", want: "custom-qwen"},
		{id: "deepseek.custom-qwen-v1:0", want: ""},
		{id: "qwen.unowned-private-v1:0", want: "unowned-private"},
	}

	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			if got := models.ProviderAlias(granted, tc.id); got != tc.want {
				t.Errorf("ProviderAlias(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}
