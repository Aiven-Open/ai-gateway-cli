// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// withPins adopts model pins from the project's Claude settings, local over shared. The overlay outranks project settings,
// so without this it would silently override them. Pins are not checked against the granted aliases.
func withPins(s slots, workDir string) slots {
	for _, name := range []string{"settings.json", "settings.local.json"} {
		env := projectEnv(filepath.Join(workDir, ".claude", name))
		s.main = pin(env, "ANTHROPIC_MODEL", s.main)
		s.opus = pin(env, "ANTHROPIC_DEFAULT_OPUS_MODEL", s.opus)
		s.sonnet = pin(env, "ANTHROPIC_DEFAULT_SONNET_MODEL", s.sonnet)
		s.haiku = pin(env, "ANTHROPIC_DEFAULT_HAIKU_MODEL", s.haiku)
		s.fable = pin(env, "ANTHROPIC_DEFAULT_FABLE_MODEL", s.fable)
	}
	return s
}

// projectEnv returns the `env` object of a settings file; unreadable or malformed files yield none.
func projectEnv(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var settings struct {
		Env map[string]any `json:"env"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return nil
	}
	return settings.Env
}

// pin returns the string pinned under key without its `[1m]` suffix, else current.
func pin(env map[string]any, key, current string) string {
	if v, _ := env[key].(string); strings.TrimSuffix(v, suffix) != "" {
		return strings.TrimSuffix(v, suffix)
	}
	return current
}
