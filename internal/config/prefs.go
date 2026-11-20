// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

// ModelPrefsFile stores per-gateway, per-tool default model aliases chosen during setup so
// `aiven-ai-gateway launch` honors them even when native defaults were left unchanged.
const ModelPrefsFile = ".config/aiven/ai-gateway-models.json"

// ModelPrefsPath returns the path to the model preferences file under userHome.
func ModelPrefsPath(userHome string) string {
	return filepath.Join(userHome, filepath.FromSlash(ModelPrefsFile))
}

// LoadModelPrefs reads the per-gateway, per-tool model preference map from userHome.
// A missing file returns an empty map and a nil error.
func LoadModelPrefs(userHome string) (map[string]map[string]string, error) {
	if userHome == "" {
		return nil, ErrNoHome
	}
	path := ModelPrefsPath(userHome)
	data, err := fsutil.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read model preferences: %w", err)
	}
	prefs := map[string]map[string]string{}
	if len(bytes.TrimSpace(data)) == 0 {
		return prefs, nil
	}
	if err := json.Unmarshal(data, &prefs); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON; fix or remove it, then retry", path)
	}
	if prefs == nil {
		prefs = map[string]map[string]string{}
	}
	return prefs, nil
}

// LoadModelPref returns the persisted model alias for (origin, tool), or "" when absent.
func LoadModelPref(userHome, origin, tool string) (string, error) {
	prefs, err := LoadModelPrefs(userHome)
	if err != nil {
		return "", err
	}
	return prefs[origin][tool], nil
}

// SaveModelPrefs atomically merges updates (tool -> alias) for origin into userHome's
// model preference file with mode 0600. An empty alias removes that tool's entry.
func SaveModelPrefs(
	userHome, origin string,
	updates map[string]string,
	write func(path string, data []byte, perm os.FileMode) error,
) error {
	if len(updates) == 0 {
		return nil
	}
	if userHome == "" {
		return ErrNoHome
	}
	prefs, err := LoadModelPrefs(userHome)
	if err != nil {
		return err
	}
	gw := prefs[origin]
	if gw == nil {
		gw = map[string]string{}
	}
	for tool, alias := range updates {
		if alias == "" {
			delete(gw, tool)
		} else {
			gw[tool] = alias
		}
	}
	if len(gw) == 0 {
		delete(prefs, origin)
	} else {
		prefs[origin] = gw
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(prefs); err != nil {
		return err
	}
	if write == nil {
		write = fsutil.WriteAtomic
	}
	return write(ModelPrefsPath(userHome), buf.Bytes(), 0o600)
}
