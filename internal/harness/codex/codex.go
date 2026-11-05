// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package codex prepares a Codex CLI launch against the gateway.
package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/pelletier/go-toml/v2"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

// Setup merges the aiven provider into ~/.codex/config.toml and writes the `aiven` profile beside it, leaving the
// user's default provider alone unless in.Default asks to replace it. The provider runs the key helper, which reads the
// gateway's key file.
func Setup(in harness.Input) (harness.Plan, error) {
	if in.UserHome == "" {
		return harness.Plan{}, config.ErrNoHome
	}
	dir := filepath.Join(in.UserHome, ".codex")
	path := filepath.Join(dir, "config.toml")
	src, cfg, err := readConfig(path)
	if err != nil {
		return harness.Plan{}, err
	}
	profilePath := filepath.Join(dir, "aiven.config.toml")
	profileSrc, profile, err := readConfig(profilePath)
	if err != nil {
		return harness.Plan{}, err
	}
	// Codex refuses any top-level profile; removing one that names another profile would drop the user's choice.
	if p, set := cfg["profile"]; set && p != "aiven" {
		return harness.Plan{}, fmt.Errorf("%s sets profile, which Codex no longer supports; remove it and select that "+
			"profile with `codex --profile <name>`, which reads <name>.config.toml, then retry", path)
	}
	// Codex refuses `-p aiven` while config.toml still declares that profile itself, so the legacy forms go, their
	// settings moving to the profile file unless it sets them already.
	delete(cfg, "profile")
	if profiles, ok := cfg["profiles"].(map[string]any); ok {
		legacy, _ := profiles["aiven"].(map[string]any)
		for key, value := range legacy {
			if _, set := profile[key]; !set {
				profile[key] = value
			}
		}
		if delete(profiles, "aiven"); len(profiles) == 0 {
			delete(cfg, "profiles")
		}
	}
	command, args := creds.Command(in.BaseURL, runtime.GOOS)
	provider := map[string]any{
		"name": "Aiven AI Gateway", "base_url": in.BaseURL + "/v1", "wire_api": "responses", "requires_openai_auth": false,
		// Codex runs auth without a shell and again after a 401, so a replaced key file takes effect mid-session.
		"auth": map[string]any{"command": command, "args": args},
	}
	if err := wireProvider(cfg, path, provider); err != nil {
		return harness.Plan{}, err
	}
	if err := wireProvider(profile, profilePath, provider); err != nil {
		return harness.Plan{}, err
	}
	if model, _ := profile["model"].(string); model == "" {
		profile["model"] = models.CodexOrFirst(in.Table)
	}
	profile["model_provider"] = "aiven"
	// Codex declares its hosted web search on every request to a custom provider, and the gateway refuses it on
	// Gemini models. Canonical config keeps the user's own setting, even when Aiven becomes the default.
	profile["web_search"] = "disabled"
	if in.Default {
		cfg["model_provider"], cfg["model"] = "aiven", profile["model"]
	}
	merged, err := edit(path, src, cfg)
	if err != nil {
		return harness.Plan{}, err
	}
	profileData, err := edit(profilePath, profileSrc, profile)
	if err != nil {
		return harness.Plan{}, err
	}
	return harness.Plan{
		Files: []harness.File{
			{Path: path, Data: merged, Backup: true},
			{Path: profilePath, Data: profileData, Backup: true},
		},
	}, nil
}

func readConfig(path string) ([]byte, map[string]any, error) {
	data, err := fsutil.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	cfg := map[string]any{}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, nil, fmt.Errorf("%s is not valid TOML; fix or move it, then retry", path)
	}
	return data, cfg, nil
}

func wireProvider(cfg map[string]any, path string, owned map[string]any) error {
	if cfg["model_providers"] == nil {
		cfg["model_providers"] = map[string]any{}
	}
	providers, ok := cfg["model_providers"].(map[string]any)
	if !ok {
		return fmt.Errorf("%s: %q is not a TOML table; fix or move it, then retry", path, "model_providers")
	}
	if providers["aiven"] == nil {
		providers["aiven"] = map[string]any{}
	}
	provider, ok := providers["aiven"].(map[string]any)
	if !ok {
		return fmt.Errorf("%s: %q is not a TOML table; fix or move it, then retry", path, "model_providers.aiven")
	}
	for field, value := range owned {
		provider[field] = value
	}
	// Codex refuses auth beside another key source, and the helper is to be the only one.
	delete(provider, "env_key")
	delete(provider, "experimental_bearer_token")
	return nil
}

// Launch selects the configured Aiven profile without rewriting provider metadata or defaults.
func Launch(in harness.Input) (harness.Plan, error) {
	return harness.Plan{Argv: append([]string{"codex", "-p", "aiven"}, in.Args...)}, nil
}

// Ready checks the provider and native profile consumed by Codex, including compatible manual configuration.
func Ready(home, baseURL string) (bool, error) {
	// An empty home would make the paths relative, naming files in the working directory.
	if home == "" {
		return false, config.ErrNoHome
	}
	var provider map[string]any
	for _, name := range []string{"config.toml", "aiven.config.toml"} {
		data, err := fsutil.ReadFile(filepath.Join(home, ".codex", name))
		if errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("read %s: %w", name, err)
		} else if err != nil {
			return false, err
		}
		cfg := map[string]any{}
		if err := toml.Unmarshal(data, &cfg); err != nil {
			return false, fmt.Errorf("%s is not valid TOML", name)
		}
		profiles, _ := cfg["profiles"].(map[string]any)
		if _, legacy := profiles["aiven"]; name == "config.toml" && (cfg["profile"] == "aiven" || legacy) {
			return false, errors.New("config.toml declares the aiven profile itself, which makes Codex refuse -p aiven")
		}
		providers, _ := cfg["model_providers"].(map[string]any)
		if p, exists := providers["aiven"]; exists {
			fields, ok := p.(map[string]any)
			if !ok {
				return false, fmt.Errorf("%s has incompatible Aiven provider wiring", name)
			}
			if provider == nil {
				provider = map[string]any{}
			}
			for key, value := range fields {
				provider[key] = value
			}
		}
		model, _ := cfg["model"].(string)
		if name == "aiven.config.toml" && (cfg["model_provider"] != "aiven" || model == "") {
			return false, errors.New("aiven.config.toml has no Aiven model selection")
		}
	}
	auth, _ := provider["auth"].(map[string]any)
	args, _ := auth["args"].([]any)
	command, want := creds.Command(baseURL, runtime.GOOS)
	helper := auth["command"] == command && slices.EqualFunc(args, want, func(a any, w string) bool { return a == w })
	envKey, hasAuth := provider["env_key"], provider["auth"] != nil
	// Codex refuses a provider whose auth command comes with env_key, a bearer token or requires_openai_auth.
	if provider["base_url"] != baseURL+"/v1" || provider["wire_api"] != "responses" ||
		envKey != nil && envKey != creds.EnvVar || hasAuth && !helper || (envKey != nil) == hasAuth ||
		hasAuth && (provider["experimental_bearer_token"] != nil || provider["requires_openai_auth"] == true) {
		return false, errors.New("config.toml or aiven.config.toml has incompatible Aiven provider wiring")
	}
	return provider["env_key"] == creds.EnvVar, nil
}
