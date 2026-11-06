// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package opencode prepares an OpenCode launch against the gateway.
package opencode

import (
	"path/filepath"

	"github.com/Aiven-Open/ai-gateway-cli/internal/harness"
	"github.com/Aiven-Open/ai-gateway-cli/internal/models"
)

// model is one entry of OpenCode's provider `models` (v1.18.25 `config/provider.ts`): `limit` needs both fields, and
// `output: 0` is OpenCode's default output cap; `cost` is USD per 1M tokens and needs both fields.
type model struct {
	Name  string `json:"name"`
	Limit *limit `json:"limit,omitempty"`
	Cost  *cost  `json:"cost,omitempty"`
}

type limit struct {
	Context int `json:"context"`
	Output  int `json:"output"`
}

type cost struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// Prepare renders a private config naming every granted alias; the provider reads the key from the environment.
func Prepare(in harness.Input) harness.Plan {
	list := map[string]model{}
	for _, m := range in.Table {
		e := model{Name: m.PriceLabel()}
		if m.Context != nil {
			e.Limit = &limit{Context: *m.Context}
		}
		if m.Priced() {
			e.Cost = &cost{Input: *m.InUSD1M, Output: *m.OutUSD1M}
		}
		list[m.Alias] = e
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
