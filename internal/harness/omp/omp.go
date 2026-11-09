// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package omp prepares an OMP launch as a role router against the gateway.
package omp

import (
	"os"
	"path/filepath"

	"github.com/Aiven-Open/ai-gateway-cli/internal/harness"
	"github.com/Aiven-Open/ai-gateway-cli/internal/models"
)

const (
	// Profile is the OMP profile the router runs in; the plugin is installed there.
	Profile = "aiven-llm"
	// routerTools is all the router session may use: edits go through role agents.
	routerTools = "read,grep,glob,task,todo"
)

// PluginHint tells how to install the router plugin into the profile.
const PluginHint = `aiven-gateway-router is not installed in the aiven-llm profile, so there are no role agents
yet. The plugin is not published yet; OMP launches need it installed in that profile.
`

// PluginInstalled reports whether the router plugin is in the profile; plugins are profile-scoped, so a user-scope install does not count.
func PluginInstalled(ompHome string) bool {
	_, err := os.Stat(filepath.Join(ompHome, "profiles", Profile, "plugins", "node_modules", "aiven-gateway-router"))
	return err == nil
}

// Input is everything Prepare needs; the table is non-empty.
type Input struct {
	Table   []models.Model
	Key     string
	BaseURL string
	// Home is the launcher home; the `--config` overlay lives under it.
	Home string
	// OMPHome is OMP's data directory; the profile's `models.yml` lives under it.
	OMPHome string
	// Args are forwarded to omp after the launcher's own flags.
	Args []string
}

// Prepare renders the router launch; the key travels only in the process environment.
// Both generated files are JSON, which OMP's YAML reader accepts.
func Prepare(in Input) harness.Plan {
	overlay := filepath.Join(in.Home, "omp", "config.yml")
	provider := map[string]any{"providers": map[string]any{"aiven-gateway": map[string]any{
		"baseUrl": in.BaseURL + "/v1", "apiKey": "AIVEN_GATEWAY_KEY", "api": "openai-completions",
		"discovery": map[string]any{"type": "openai-models-list"},
	}}}
	config := map[string]any{
		"modelRoles":    map[string]any{"default": "aiven-gateway/" + routerAlias(in.Table)},
		"enabledModels": []string{"aiven-gateway/*"},
	}
	return harness.Plan{
		Files: []harness.File{
			{Path: filepath.Join(in.OMPHome, "profiles", Profile, "agent", "models.yml"), Data: harness.JSON(provider)},
			{Path: overlay, Data: harness.JSON(config)},
		},
		Notice: models.Table(in.Table),
		Argv:   append([]string{"omp", "--profile", Profile, "--config", overlay, "--tools", routerTools}, in.Args...),
		Env:    []string{"AIVEN_GATEWAY_KEY=" + in.Key},
	}
}

// routerAlias is the cheap tier the router session itself runs on; it is a pinned role default, so a router alias only when the key grants nothing else.
func routerAlias(table []models.Model) string {
	if a := models.FirstGranted(table, "claude-haiku-5-5", "claude-haiku-5.5", "deepseek-flash", "glm-fast", "glm"); a != "" {
		return a
	}
	return models.FirstAlias(table)
}
