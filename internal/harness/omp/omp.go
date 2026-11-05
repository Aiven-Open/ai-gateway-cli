// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package omp prepares the ordinary OMP coding harness against the gateway.
package omp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

// APIKey makes OMP run the key helper of the gateway at baseURL: OMP runs a value starting with ! through /bin/sh,
// or on Windows its embedded bash-like shell, and caches its output until a 401.
func APIKey(baseURL string) string { return "!" + creds.CommandLine(baseURL, runtime.GOOS) }

// Model is the --model value that picks the gateway's default alias.
func Model(table []models.Model) string { return "aiven/" + models.SonnetOrFirst(table) }

// Setup updates the YAML OMP reads; only an explicit default choice changes modelRoles.
func Setup(in harness.Input) (harness.Plan, error) {
	if in.UserHome == "" {
		return harness.Plan{}, config.ErrNoHome
	}
	dir := filepath.Join(in.UserHome, ".omp", "agent")
	modelPath, err := nativePath(dir, "models")
	if err != nil {
		return harness.Plan{}, err
	}
	configPath, err := nativePath(dir, "config")
	if err != nil {
		return harness.Plan{}, err
	}
	for _, legacy := range []struct{ path, native string }{
		{filepath.Join(dir, "models.json"), modelPath},
		{filepath.Join(dir, "settings.json"), configPath},
	} {
		if _, err := os.Stat(legacy.native); errors.Is(err, os.ErrNotExist) {
			if _, err := os.Lstat(legacy.path); err == nil {
				return harness.Plan{}, fmt.Errorf("%s awaits native OMP migration; run omp once, then rerun setup. If migration fails, fix the legacy file first", legacy.path)
			} else if !errors.Is(err, os.ErrNotExist) {
				return harness.Plan{}, fmt.Errorf("inspect %s: %w", legacy.path, err)
			}
		}
	}
	if _, err := readConfig(configPath); err != nil {
		return harness.Plan{}, err
	}
	var list []map[string]any
	for _, m := range models.Sorted(in.Table) {
		protocol := models.ModelProtocol(m.Alias)
		list = append(list, map[string]any{
			"id": m.Alias, "name": m.DisplayName(), "api": string(protocol),
			"baseUrl": protocol.BaseURL(in.BaseURL),
		})
	}
	data, err := mergeConfig(modelPath, "providers", "aiven", map[string]any{
		"baseUrl": in.BaseURL + "/v1", "apiKey": APIKey(in.BaseURL), "auth": "apiKey", "models": list,
	})
	if err != nil {
		return harness.Plan{}, err
	}
	files := []harness.File{{Path: modelPath, Data: data, Backup: true, Models: true}}
	if in.Default {
		data, err = mergeConfig(configPath, "modelRoles", "default", Model(in.Table))
		if err != nil {
			return harness.Plan{}, err
		}
		files = append(files, harness.File{Path: configPath, Data: data, Backup: true})
	}
	return harness.Plan{Files: files}, nil
}

// Launch selects the configured default model when there are no native args, and passes native args unchanged.
func Launch(in harness.Input) (harness.Plan, error) {
	args := harness.ModelArgs(in.Args, Model(in.Table), "--model")
	if len(args) != len(in.Args) {
		if in.UserHome == "" {
			return harness.Plan{}, config.ErrNoHome
		}
		p, err := provider(in.UserHome)
		if err != nil {
			return harness.Plan{}, err
		}
		list, _ := p["models"].([]any)
		found := false
		for _, entry := range list {
			m, _ := entry.(map[string]any)
			found = found || m["id"] == models.SonnetOrFirst(in.Table)
		}
		if !found {
			return harness.Plan{}, errors.New("selected default is absent from the configured catalogue; refresh with setup")
		}
	}
	return harness.Plan{Argv: append([]string{"omp"}, args...)}, nil
}

func provider(home string) (map[string]any, error) {
	path, err := nativePath(filepath.Join(home, ".omp", "agent"), "models")
	if err != nil {
		return nil, err
	}
	cfg, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	providers, _ := cfg["providers"].(map[string]any)
	p, _ := providers["aiven"].(map[string]any)
	return p, nil
}

// Ready validates native-consumed wiring without interpreting project or scope overrides.
func Ready(home, baseURL string) (bool, error) {
	// An empty home would make the paths relative, naming files in the working directory.
	if home == "" {
		return false, config.ErrNoHome
	}
	configPath, err := nativePath(filepath.Join(home, ".omp", "agent"), "config")
	if err != nil {
		return false, err
	}
	if _, err := readConfig(configPath); err != nil {
		return false, err
	}
	p, err := provider(home)
	if err != nil {
		return false, err
	}
	list, _ := p["models"].([]any)
	if p["apiKey"] != APIKey(baseURL) && p["apiKey"] != creds.EnvVar || p["auth"] != "apiKey" || len(list) == 0 {
		return false, errors.New("normal OMP models YAML has no compatible Aiven provider")
	}
	for _, entry := range list {
		m, _ := entry.(map[string]any)
		id, _ := m["id"].(string)
		protocol := models.ModelProtocol(id)
		if id == "" || m["api"] != string(protocol) || m["baseUrl"] != protocol.BaseURL(baseURL) {
			return false, errors.New("normal OMP models YAML has incompatible Aiven model wiring")
		}
	}
	return p["apiKey"] == creds.EnvVar, nil
}
