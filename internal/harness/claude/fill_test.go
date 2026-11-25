// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package claude

import (
	"slices"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

// catalogue grants aliases with no owner, as a gateway relaying a bare listing does.
func catalogue(aliases ...string) []models.Model {
	out := make([]models.Model, len(aliases))
	for i, a := range aliases {
		out[i] = models.Model{Alias: a}
	}
	return out
}

// withAnthropicOwner is ms with every claude- alias owned by Anthropic.
func withAnthropicOwner(ms []models.Model) []models.Model {
	out := slices.Clone(ms)
	for i, m := range out {
		if strings.HasPrefix(m.Alias, "claude-") {
			out[i].Owner = "anthropic"
		}
	}
	return out
}

func picks(main string, aliases ...string) harness.ModelSelection {
	return harness.ModelSelection{Model: main, Models: aliases}
}

func TestResolveSlotsFillsChosenModelsByRule(t *testing.T) {
	t.Parallel()
	full := catalogue("claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5",
		"claude-fable-5-2", "gemini-4-neon", "gpt-6.2-astra", "deepseek-v4.1-flash",
		"glm-5", "kimi-k3", "qwen3-coder-480b")
	aivenOpus := append(catalogue("claude-sonnet-5-5", "claude-haiku-4-5", "glm-5"),
		models.Model{Alias: "claude-opus-5-5", Owner: "Aiven"})
	for _, tc := range []struct {
		name  string
		table []models.Model
		sel   harness.ModelSelection
		env   map[string]any
		want  slots
	}{
		{
			name:  "the five models of the spec example",
			table: full,
			sel: picks("gemini-4-neon", "claude-fable-5-2", "claude-haiku-5-5",
				"gemini-4-neon", "gpt-6.2-astra", "deepseek-v4.1-flash"),
			want: slots{main: "gemini-4-neon", custom: "gemini-4-neon", fable: "claude-fable-5-2",
				opus: "gpt-6.2-astra", sonnet: "deepseek-v4.1-flash", haiku: "claude-haiku-5-5"},
		},
		{
			name:  "five others displace the Claude tiers, haiku last",
			table: full,
			sel: picks("glm-5", "glm-5", "gemini-4-neon", "gpt-6.2-astra",
				"deepseek-v4.1-flash", "kimi-k3"),
			want: slots{main: "glm-5", custom: "glm-5", fable: "gemini-4-neon",
				opus: "gpt-6.2-astra", sonnet: "deepseek-v4.1-flash", haiku: "kimi-k3"},
		},
		{
			name:  "tiers without a granted Claude model fill first",
			table: catalogue("claude-sonnet-5-5", "claude-haiku-5-5", "glm-5", "gpt-6.2-astra", "kimi-k3"),
			sel:   picks("glm-5", "glm-5", "gpt-6.2-astra", "kimi-k3"),
			want: slots{main: "glm-5", custom: "glm-5", fable: "gpt-6.2-astra", opus: "kimi-k3",
				sonnet: "claude-sonnet-5-5", haiku: "claude-haiku-5-5"},
		},
		{
			name:  "a key without Claude models opens every slot; spare tiers take the default",
			table: catalogue("glm-5", "gpt-6.2-astra"),
			sel:   picks("gpt-6.2-astra", "glm-5", "gpt-6.2-astra"),
			want: slots{main: "gpt-6.2-astra", custom: "glm-5", fable: "gpt-6.2-astra",
				opus: "gpt-6.2-astra", sonnet: "gpt-6.2-astra", haiku: "gpt-6.2-astra"},
		},
		{
			name:  "Claude picks alone take only their own tiers",
			table: full,
			sel:   picks("claude-opus-5-5", "claude-opus-5-5", "claude-haiku-5-5"),
			want: slots{main: "claude-opus-5-5", fable: "claude-fable-5-2", opus: "claude-opus-5-5",
				sonnet: "claude-sonnet-5-5", haiku: "claude-haiku-5-5"},
		},
		{
			name:  "a Claude default keeps the custom slot for the first other pick",
			table: full,
			sel:   picks("claude-sonnet-5-5", "claude-sonnet-5-5", "glm-5"),
			want: slots{main: "claude-sonnet-5-5", custom: "glm-5", fable: "claude-fable-5-2",
				opus: "claude-opus-5-5", sonnet: "claude-sonnet-5-5", haiku: "claude-haiku-5-5"},
		},
		{
			name:  "pins in the existing settings do not change the tiers",
			table: catalogue("claude-sonnet-5-5", "claude-sonnet-4-5", "glm-5"),
			env:   map[string]any{"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-4-5"},
			sel:   picks("glm-5", "glm-5"),
			want: slots{main: "glm-5", custom: "glm-5", fable: "claude-sonnet-5-5",
				opus: "claude-sonnet-5-5", sonnet: "claude-sonnet-5-5", haiku: "claude-sonnet-5-5"},
		},
		{
			name:  "a pin to another vendor's model in the settings is ignored",
			table: catalogue("claude-opus-5-5", "claude-haiku-5-5", "glm-5", "gpt-6.2-astra"),
			env:   map[string]any{"ANTHROPIC_DEFAULT_SONNET_MODEL": "gpt-6.2-astra"},
			sel:   picks("glm-5", "glm-5", "gpt-6.2-astra"),
			want: slots{main: "glm-5", custom: "glm-5", fable: "claude-opus-5-5",
				opus: "claude-opus-5-5", sonnet: "gpt-6.2-astra", haiku: "claude-haiku-5-5"},
		},
		{
			name:  "a ticked Claude model takes the tier its alias names",
			table: full,
			sel:   picks("claude-fable-5-2", "claude-fable-5-2", "glm-5"),
			want: slots{main: "claude-fable-5-2", custom: "glm-5", fable: "claude-fable-5-2",
				opus: "claude-opus-5-5", sonnet: "claude-sonnet-5-5", haiku: "claude-haiku-5-5"},
		},
		{
			name: "two ticked opus models: the first takes the tier",
			table: catalogue("claude-opus-5-5", "claude-opus-4-6", "claude-opus-4-5",
				"claude-sonnet-5-5", "claude-haiku-5-5", "glm-5"),
			sel: picks("glm-5", "claude-opus-4-6", "claude-opus-4-5", "glm-5"),
			want: slots{main: "glm-5", custom: "glm-5", fable: "claude-opus-5-5",
				opus: "claude-opus-4-6", sonnet: "claude-sonnet-5-5", haiku: "claude-haiku-5-5"},
		},
		{
			name: "a ticked Claude model that loses its tier may stay where the preset put it",
			table: catalogue("claude-opus-5-5", "claude-opus-4-6", "claude-sonnet-5-5",
				"claude-haiku-5-5", "glm-5"),
			sel: picks("glm-5", "claude-opus-4-6", "claude-opus-5-5", "glm-5"),
			want: slots{main: "glm-5", custom: "glm-5", fable: "claude-opus-5-5",
				opus: "claude-opus-4-6", sonnet: "claude-sonnet-5-5", haiku: "claude-haiku-5-5"},
		},
		{
			name:  "a ticked Claude model wins over the tier's preset model",
			table: catalogue("claude-sonnet-5-5", "claude-sonnet-4-5", "glm-5"),
			sel:   picks("glm-5", "claude-sonnet-4-5", "glm-5"),
			want: slots{main: "glm-5", custom: "glm-5", fable: "claude-sonnet-5-5",
				opus: "claude-sonnet-5-5", sonnet: "claude-sonnet-4-5", haiku: "claude-sonnet-5-5"},
		},
		{
			name: "a ticked Claude model whose alias names no tier gets no slot",
			table: catalogue("claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5",
				"claude-mythos-1", "glm-5"),
			sel: picks("glm-5", "claude-mythos-1", "glm-5"),
			want: slots{main: "glm-5", custom: "glm-5", fable: "claude-opus-5-5",
				opus: "claude-opus-5-5", sonnet: "claude-sonnet-5-5", haiku: "claude-haiku-5-5"},
		},
		{
			name:  "a Claude pick and five others: the others take every slot",
			table: full,
			sel: picks("glm-5", "claude-fable-5-2", "glm-5", "gemini-4-neon", "gpt-6.2-astra",
				"deepseek-v4.1-flash", "kimi-k3"),
			want: slots{main: "glm-5", custom: "glm-5", fable: "deepseek-v4.1-flash",
				opus: "gemini-4-neon", sonnet: "gpt-6.2-astra", haiku: "kimi-k3"},
		},
		{
			name:  "a ticked fable and four others: haiku still goes last",
			table: full,
			sel: picks("glm-5", "claude-fable-5-2", "glm-5", "gemini-4-neon", "gpt-6.2-astra",
				"deepseek-v4.1-flash"),
			want: slots{main: "glm-5", custom: "glm-5", fable: "deepseek-v4.1-flash",
				opus: "gemini-4-neon", sonnet: "gpt-6.2-astra", haiku: "claude-haiku-5-5"},
		},
		{
			name:  "a granted fable keeps its tier when only another vendor's model is ticked",
			table: catalogue("claude-fable-5-2", "gemini-4-neon"),
			sel:   picks("gemini-4-neon", "gemini-4-neon"),
			want: slots{main: "gemini-4-neon", custom: "gemini-4-neon", fable: "claude-fable-5-2",
				opus: "gemini-4-neon", sonnet: "gemini-4-neon", haiku: "gemini-4-neon"},
		},
		{
			name:  "an Aiven-owned claude- alias is another vendor's model and is not placed by tier name",
			table: aivenOpus,
			sel:   picks("claude-opus-5-5", "claude-opus-5-5", "glm-5"),
			want: slots{main: "claude-opus-5-5", custom: "claude-opus-5-5", fable: "glm-5",
				sonnet: "claude-sonnet-5-5", haiku: "claude-haiku-4-5"},
		},
		{
			name:  "an Aiven-owned claude- alias picked alone fills one slot and no tier default",
			table: aivenOpus,
			sel:   picks("claude-opus-5-5", "claude-opus-5-5"),
			want: slots{main: "claude-opus-5-5", custom: "claude-opus-5-5",
				sonnet: "claude-sonnet-5-5", haiku: "claude-haiku-4-5"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, explicit, err := resolveSlots(tc.table, tc.sel, tc.env)
			if err != nil || !explicit || got != tc.want {
				t.Errorf("resolveSlots = %+v, %v, %v; want %+v", got, explicit, err, tc.want)
			}
		})
	}
}

func TestResolveSlotsGivesSameSlotsWhenFedItsOwnResult(t *testing.T) {
	t.Parallel()
	table := catalogue("claude-sonnet-5-5", "glm-5", "gpt-6.2-astra", "kimi-k3", "gemini-4-neon")
	sel := picks("glm-5", "glm-5", "gpt-6.2-astra", "kimi-k3", "gemini-4-neon")
	first, _, err := resolveSlots(table, sel, nil)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]any{
		"ANTHROPIC_DEFAULT_FABLE_MODEL":  first.fable,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   first.opus,
		"ANTHROPIC_DEFAULT_SONNET_MODEL": first.sonnet,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  first.haiku,
	}
	second, _, err := resolveSlots(table, sel, env)
	if err != nil || second != first {
		t.Errorf("rerun = %+v, %v; want %+v", second, err, first)
	}
}

func TestResolveSlotsRefusesSixthModelFromAnotherVendor(t *testing.T) {
	t.Parallel()
	table := catalogue("claude-sonnet-5-5", "gemini-4-neon", "gpt-6.2-astra",
		"deepseek-v4.1-flash", "glm-5", "kimi-k3", "qwen3-coder-480b")
	sel := picks("glm-5", "claude-sonnet-5-5", "gemini-4-neon", "gpt-6.2-astra",
		"deepseek-v4.1-flash", "glm-5", "kimi-k3", "qwen3-coder-480b")
	if _, _, err := resolveSlots(table, sel, nil); err == nil ||
		!strings.Contains(err.Error(), "6 models from other vendors chosen") {
		t.Fatalf("err = %v, want the limit", err)
	}
}

func TestAivenOwnedClaudeAliasCountsTowardLimit(t *testing.T) {
	t.Parallel()
	table := append(
		catalogue("gemini-4-neon", "gpt-6.2-astra", "deepseek-v4.1-flash", "glm-5", "kimi-k3"),
		models.Model{Alias: "claude-opus-5-5", Owner: "Aiven"},
	)
	sel := picks("glm-5", "claude-opus-5-5", "gemini-4-neon", "gpt-6.2-astra",
		"deepseek-v4.1-flash", "glm-5", "kimi-k3")
	if _, _, err := resolveSlots(table, sel, nil); err == nil ||
		!strings.Contains(err.Error(), "6 models from other vendors chosen") {
		t.Fatalf("err = %v, want the limit", err)
	}
	if IsClaude(table, "claude-opus-5-5") ||
		!IsClaude(catalogue("claude-opus-5-5"), "claude-opus-5-5") ||
		IsClaude(table, "glm-5") ||
		IsClaude(table, "claude-not-granted") {
		t.Error("IsClaude must hold only for the catalogue's Anthropic models")
	}
}

func TestSpecExampleHoldsWithAnthropicOwnerAndWithNone(t *testing.T) {
	t.Parallel()
	bare := catalogue("claude-fable-5-2", "claude-haiku-5-5", "gemini-4-neon", "gpt-6.2-astra",
		"deepseek-v4.1-flash")
	sel := picks("gemini-4-neon", "claude-fable-5-2", "claude-haiku-5-5", "gemini-4-neon",
		"gpt-6.2-astra", "deepseek-v4.1-flash")
	want := slots{main: "gemini-4-neon", custom: "gemini-4-neon", fable: "claude-fable-5-2",
		opus: "gpt-6.2-astra", sonnet: "deepseek-v4.1-flash", haiku: "claude-haiku-5-5"}
	for name, table := range map[string][]models.Model{
		"no owner": bare, "Anthropic owner": withAnthropicOwner(bare),
	} {
		got, _, err := resolveSlots(table, sel, nil)
		if err != nil || got != want {
			t.Errorf("%s: slots = %+v, %v; want %+v", name, got, err, want)
		}
	}
}

func TestIsClaudeTakesUnownedClaudeAliasButNotAnotherVendors(t *testing.T) {
	t.Parallel()
	table := append(catalogue("claude-fable-5-2", "claude-next", "glm-5", "mystery"),
		models.Model{Alias: "claude-opus-5-5", Owner: "Aiven"})
	for alias, want := range map[string]bool{
		"claude-fable-5-2": true, "claude-next": true, "glm-5": false, "mystery": false,
		"claude-opus-5-5": false,
	} {
		if got := IsClaude(table, alias); got != want {
			t.Errorf("IsClaude(%s) = %v, want %v", alias, got, want)
		}
	}
}

func TestKeepGrantedClaudeTierFallsBackFromGrantedNonClaudePin(t *testing.T) {
	t.Parallel()
	const key = "ANTHROPIC_DEFAULT_FABLE_MODEL"
	got := keepGrantedClaudeTier(catalogue("claude-fable-5-2", "glm-5"),
		map[string]any{key: "glm-5"}, key, "fallback")
	if got != "fallback" {
		t.Errorf("pin = %q, want the fallback, not a non-Claude model", got)
	}
}

func TestKeepGrantedClaudeTierKeepsUnownedClaudePin(t *testing.T) {
	t.Parallel()
	const key = "ANTHROPIC_DEFAULT_FABLE_MODEL"
	got := keepGrantedClaudeTier(catalogue("claude-fable-5-2"),
		map[string]any{key: "claude-fable-5-2"}, key, "fallback")
	if got != "claude-fable-5-2" {
		t.Errorf("pin = %q, want claude-fable-5-2", got)
	}
}
