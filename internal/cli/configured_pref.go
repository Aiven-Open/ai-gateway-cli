// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

// launcherPrefModel is a Pi, OpenCode or OMP launcher preference the key still grants, as a configured model, so a
// single-tool rerun offers to keep it as the multi-tool path already does. Other tools have none.
func launcherPrefModel(env config.Env, table []models.Model, tool string) configuredModel {
	pref := grantedModelPref(env, table, tool)
	if pref == "" {
		return configuredModel{}
	}
	return configuredModel{
		present: true,
		granted: true,
		file:    config.ModelPrefsPath(env.UserHome),
		setting: tool,
		model:   pref,
	}
}
