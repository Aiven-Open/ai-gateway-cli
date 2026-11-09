// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package omp prepares an OMP launch as a role router against the gateway.
package omp

import (
	"os"
	"path/filepath"

	"github.com/Aiven-Open/ai-gateway-cli/internal/harness"
	"github.com/Aiven-Open/ai-gateway-cli/internal/models"
)

// routerTools is all the router session may use: edits go through role agents.
const routerTools = "read,grep,glob,task,todo"

// Profile is the OMP profile the router runs in for a gateway scope (see `config.Env.GatewayScope`); its
// `models.yml` names the gateway, so each gateway gets its own, and the plugin is installed per profile.
func Profile(scope string) string {
	if scope == "" {
		return "aiven-llm"
	}
	return "aiven-llm-" + scope
}

// PluginHint tells that the router plugin is missing from profile.
func PluginHint(profile string) string {
	return "aiven-gateway-router is not installed in the " + profile + ` profile, so there are no role agents
yet. The plugin is not published yet; OMP launches need it installed in that profile.
`
}

// PluginInstalled reports whether the router plugin is in the profile; plugins are profile-scoped, so a user-scope install does not count.
// OMP 18.0.11 moves a named profile's data, plugins included, to $XDG_DATA_HOME/omp/profiles/<name> once that dir exists (`utils/src/dirs.ts`).
func PluginInstalled(ompHome, xdgDataHome, profile string) bool {
	root := filepath.Join(ompHome, "profiles", profile)
	if xdgDataHome != "" {
		if xdg := filepath.Join(xdgDataHome, "omp", "profiles", profile); exists(xdg) {
			root = xdg
		}
	}
	return exists(filepath.Join(root, "plugins", "node_modules", "aiven-gateway-router"))
}

func exists(path string) bool {
	_, err := os.Stat(path)
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
	// Profile is the OMP profile to run in.
	Profile string
	// Args are forwarded to omp after the launcher's own flags.
	Args []string
}

// Prepare renders the router launch; the key travels only in the process environment.
// Both generated files are JSON, which OMP's YAML reader accepts.
func Prepare(in Input) harness.Plan {
	overlay := filepath.Join(in.Home, "omp", "config.yml")
	provider := map[string]any{"providers": map[string]any{"aiven-gateway": map[string]any{
		"baseUrl": in.BaseURL + "/v1", "apiKey": "AIVEN_GATEWAY_KEY", "api": "openai-completions",
		"discovery":      map[string]any{"type": "openai-models-list"},
		"modelOverrides": overrides(in.Table),
	}}}
	config := map[string]any{
		"modelRoles":    map[string]any{"default": "aiven-gateway/" + routerAlias(in.Table)},
		"enabledModels": []string{"aiven-gateway/*"},
	}
	return harness.Plan{
		Files: []harness.File{
			{Path: filepath.Join(in.OMPHome, "profiles", in.Profile, "agent", "models.yml"), Data: harness.JSON(provider)},
			{Path: overlay, Data: harness.JSON(config)},
		},
		Notice: models.Table(in.Table),
		Argv:   append([]string{"omp", "--profile", in.Profile, "--config", overlay, "--tools", routerTools}, in.Args...),
		Env:    []string{"AIVEN_GATEWAY_KEY=" + in.Key},
	}
}

// overrides patches discovered models with the gateway's context window and prices (USD per 1M), which OMP's
// `openai-models-list` discovery does not read (v18.0.11 `model-discovery.ts`); unknown values keep its defaults.
func overrides(table []models.Model) map[string]any {
	out := map[string]any{}
	for _, m := range table {
		o := map[string]any{}
		if m.Context != nil {
			o["contextWindow"] = *m.Context
		}
		if m.Priced() {
			o["cost"] = map[string]float64{"input": *m.InUSD1M, "output": *m.OutUSD1M}
		}
		out[m.Alias] = o
	}
	return out
}

// routerAlias is the cheap tier the router session itself runs on; it is a pinned role default, so a router alias only when the key grants nothing else.
func routerAlias(table []models.Model) string {
	if a := models.FirstGranted(table, "claude-haiku-5-5", "claude-haiku-5.5", "deepseek-flash", "glm-fast", "glm"); a != "" {
		return a
	}
	return models.FirstAlias(table)
}
