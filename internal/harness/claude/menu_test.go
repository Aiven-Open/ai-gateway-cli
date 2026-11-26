// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package claude_test

import (
	"reflect"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
)

func TestReadMenuListsDistinctSlotModels(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		want       claude.Menu
	}{
		{"five models", `{"model":"gpt-6.2-sol","env":{"ANTHROPIC_CUSTOM_MODEL_OPTION":"deepseek-v4.1-flash",` +
			`"ANTHROPIC_DEFAULT_FABLE_MODEL":"grok-4.7","ANTHROPIC_DEFAULT_OPUS_MODEL":"gpt-6.2-astra[1m]",` +
			`"ANTHROPIC_DEFAULT_SONNET_MODEL":"gpt-6.2-sol","ANTHROPIC_DEFAULT_HAIKU_MODEL":"claude-haiku-5-5",` +
			`"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY":"1"}}`,
			claude.Menu{
				Default:   "gpt-6.2-sol",
				Others:    []string{"deepseek-v4.1-flash", "grok-4.7", "gpt-6.2-astra", "claude-haiku-5-5"},
				Discovery: true,
			}},
		{"single model in every slot", `{"model":"claude-sonnet[1m]","env":{"ANTHROPIC_CUSTOM_MODEL_OPTION":"",` +
			`"ANTHROPIC_DEFAULT_OPUS_MODEL":"claude-sonnet[1m]"}}`, claude.Menu{Default: "claude-sonnet"}},
		{"env model first", `{"model":"a","env":{"ANTHROPIC_MODEL":"b","ANTHROPIC_DEFAULT_OPUS_MODEL":"a"}}`,
			claude.Menu{Default: "b", Others: []string{"a"}}},
	} {
		got, err := claude.ReadMenu([]byte(tc.data))
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: ReadMenu = %+v, %v; want %+v", tc.name, got, err, tc.want)
		}
	}
}
