// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package claude

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/aiven-open/ai-gateway-cli/internal/harness"
)

// menuVars put a model in /model beside the default: the custom option, then the tiers in fill order.
var menuVars = append([]string{"ANTHROPIC_CUSTOM_MODEL_OPTION"}, tierVars...)

// Menu is what a settings file setup writes offers in Claude Code's /model.
type Menu struct {
	// Default is the model a session starts on, without the [1m] suffix.
	Default string
	// Others are the distinct models the custom option and tier slots name, other than Default, in menuVars order.
	Others []string
	// Discovery is set when gateway discovery adds the key's granted Claude models.
	Discovery bool
}

// ReadMenu reads the menu from data, a settings file as setup writes it.
func ReadMenu(data []byte) (Menu, error) {
	def, err := Selected(data)
	if err != nil {
		return Menu{}, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(harness.Strip(data, harness.Strict), &cfg); err != nil {
		return Menu{}, fmt.Errorf("read Claude settings: %w", err)
	}
	menu := Menu{Default: bare(def), Discovery: discoveryOn(cfg)}
	env, _ := cfg["env"].(map[string]any)
	for _, name := range menuVars {
		v, _ := env[name].(string)
		if m := bare(v); m != "" && m != menu.Default && !slices.Contains(menu.Others, m) {
			menu.Others = append(menu.Others, m)
		}
	}
	return menu, nil
}
