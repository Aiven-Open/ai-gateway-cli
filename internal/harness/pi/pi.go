// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package pi prepares a Pi launch against the gateway.
package pi

import (
	"path/filepath"

	"github.com/Aiven-Open/ai-gateway-cli/internal/harness"
	"github.com/Aiven-Open/ai-gateway-cli/internal/models"
)

type model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Cost *cost  `json:"cost,omitempty"`
}

// cost is USD per million tokens; Pi has no cache pricing to learn from the gateway.
type cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// Prepare renders a private PI_CODING_AGENT_DIR with every granted alias; the provider reads the key from the environment.
func Prepare(in harness.Input) harness.Plan {
	var list []model
	for _, m := range models.Sorted(in.Table) {
		e := model{ID: m.Alias, Name: m.PriceLabel()}
		if m.Priced() {
			e.Cost = &cost{Input: *m.InUSD1M, Output: *m.OutUSD1M}
		}
		list = append(list, e)
	}
	cfg := map[string]any{"providers": map[string]any{"aiven-gateway": map[string]any{
		"baseUrl": in.BaseURL + "/v1", "api": "openai-completions", "apiKey": "$AIVEN_GATEWAY_KEY", "models": list,
	}}}
	settings := map[string]string{"defaultProvider": "aiven-gateway", "defaultModel": models.SonnetOrFirst(in.Table)}
	dir := filepath.Join(in.Home, "pi")
	return harness.Plan{
		Files: []harness.File{
			{Path: filepath.Join(dir, "models.json"), Data: harness.JSON(cfg)},
			{Path: filepath.Join(dir, "settings.json"), Data: harness.JSON(settings)},
		},
		Argv:   append([]string{"pi"}, in.Args...),
		Env:    []string{"PI_CODING_AGENT_DIR=" + dir, "AIVEN_GATEWAY_KEY=" + in.Key},
		Notice: models.Table(in.Table),
	}
}
