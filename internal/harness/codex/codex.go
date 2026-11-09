// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package codex prepares a Codex CLI launch against the gateway.
package codex

import (
	"cmp"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"

	"github.com/Aiven-Open/ai-gateway-cli/internal/harness"
	"github.com/Aiven-Open/ai-gateway-cli/internal/models"
)

type config struct {
	Model          string              `toml:"model"`
	ModelProvider  string              `toml:"model_provider"`
	ModelProviders map[string]provider `toml:"model_providers"`
}

type provider struct {
	Name    string `toml:"name"`
	BaseURL string `toml:"base_url"`
	EnvKey  string `toml:"env_key"`
	WireAPI string `toml:"wire_api"`
}

// Prepare renders a private CODEX_HOME whose provider reads the key from the environment.
func Prepare(in harness.Input) (harness.Plan, error) {
	data, err := toml.Marshal(config{
		Model:         cmp.Or(models.FirstGranted(in.Table, "gpt-6.1-sol", "gpt-6-sol"), models.DefaultAlias(in.Table, "gpt-")),
		ModelProvider: "aiven-gateway",
		ModelProviders: map[string]provider{"aiven-gateway": {
			Name: "Aiven LLM Gateway", BaseURL: in.BaseURL + "/v1", EnvKey: "AIVEN_GATEWAY_KEY", WireAPI: "responses",
		}},
	})
	if err != nil {
		return harness.Plan{}, err
	}
	home := filepath.Join(in.Home, "codex")
	return harness.Plan{
		Files:  []harness.File{{Path: filepath.Join(home, "config.toml"), Data: data}},
		Argv:   append([]string{"codex"}, in.Args...),
		Env:    []string{"CODEX_HOME=" + home, "AIVEN_GATEWAY_KEY=" + in.Key},
		Notice: models.Table(in.Table),
	}, nil
}
