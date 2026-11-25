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

// checkPrefsParents refuses any symlinked or non-directory component between userHome and ModelPrefsPath.
func checkPrefsParents(userHome string) error {
	cur := filepath.Clean(userHome)
	for _, part := range []string{".config", "aiven"} {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect %s: %w", cur, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is not a regular directory; fix or move it, then retry", cur)
		}
	}
	return nil
}

// LoadModelPrefs reads the per-gateway, per-tool model preference map from userHome.
// A missing file returns an empty map and a nil error. Symlinked parent directories or files are refused.
func LoadModelPrefs(userHome string) (map[string]map[string]string, error) {
	if userHome == "" {
		return nil, ErrNoHome
	}
	if err := checkPrefsParents(userHome); err != nil {
		return nil, err
	}
	path := ModelPrefsPath(userHome)
	data, err := fsutil.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read model preferences: %w", err)
	}
	return decodePrefs(path, data)
}

// decodePrefs reads data, the content of the preference file at path: a JSON object of gateways,
// each an object of tools and non-empty aliases. A null, a missing alias or trailing text is
// refused rather than read as no preference.
func decodePrefs(path string, data []byte) (map[string]map[string]string, error) {
	malformed := fmt.Errorf(
		"%s is not a valid model preference file; fix or remove it, then retry",
		path,
	)
	var raw map[string]map[string]*string
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return nil, malformed
	}
	prefs := make(map[string]map[string]string, len(raw))
	for origin, tools := range raw {
		if tools == nil {
			return nil, malformed
		}
		gw := make(map[string]string, len(tools))
		for tool, alias := range tools {
			if alias == nil || *alias == "" {
				return nil, malformed
			}
			gw[tool] = *alias
		}
		prefs[origin] = gw
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
// model preference file with mode 0600, refusing symlinked parent directories. An empty alias removes that tool.
// Callers hold LockFile; the read, merge and rename are not atomic on their own.
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
	if write != nil {
		if err := checkPrefsParents(userHome); err != nil {
			return err
		}
		return write(ModelPrefsPath(userHome), buf.Bytes(), 0o600)
	}
	dir, err := fsutil.OpenDir(userHome, filepath.Join(".config", "aiven"))
	if err != nil {
		return fmt.Errorf("write %s: %w", ModelPrefsPath(userHome), err)
	}
	defer func() { _ = dir.Close() }()
	return dir.WriteAtomic("ai-gateway-models.json", buf.Bytes(), 0o600, nil)
}
