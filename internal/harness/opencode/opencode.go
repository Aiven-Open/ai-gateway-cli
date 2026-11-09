// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package opencode prepares an OpenCode launch against the gateway.
package opencode

import (
	"path/filepath"

	"github.com/Aiven-Open/ai-gateway-cli/internal/harness"
	"github.com/Aiven-Open/ai-gateway-cli/internal/models"
)

type model struct {
	Name string `json:"name"`
}

// Prepare renders a private config naming every granted alias; the provider reads the key from the environment.
func Prepare(in harness.Input) harness.Plan {
	list := map[string]model{}
	for _, m := range in.Table {
		list[m.Alias] = model{Name: m.PriceLabel()}
	}
	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{"aiven-gateway": map[string]any{
			"npm":     "@ai-sdk/openai-compatible",
			"name":    "Aiven LLM Gateway",
			"options": map[string]string{"baseURL": in.BaseURL + "/v1", "apiKey": "{env:AIVEN_GATEWAY_KEY}"},
			"models":  list,
		}},
		"model": "aiven-gateway/" + models.SonnetOrFirst(in.Table),
	}
	path := filepath.Join(in.Home, "opencode", "opencode.json")
	return harness.Plan{
		Files: []harness.File{{Path: path, Data: harness.JSON(cfg)}},
		Argv:  append([]string{"opencode"}, in.Args...),
		Env:   []string{"OPENCODE_CONFIG=" + path, "AIVEN_GATEWAY_KEY=" + in.Key},
	}
}
