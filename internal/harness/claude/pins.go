// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package claude

import (
	"cmp"
	"path/filepath"

	"github.com/aiven-open/ai-gateway-cli/internal/harness"
)

// withPins adopts model pins from the dedicated settings cfg, then canonical user and project settings, local over
// shared. The overlay outranks native settings, so without this it would silently override them. Pins are not checked
// against the granted aliases.
func withPins(s slots, cfg map[string]any, userHome, workDir string) (slots, error) {
	layers := []map[string]any{cfg}
	for _, path := range []string{
		filepath.Join(userHome, ".claude", "settings.json"),
		filepath.Join(workDir, ".claude", "settings.json"),
		filepath.Join(workDir, ".claude", "settings.local.json"),
	} {
		settings, err := harness.ReadObject(path)
		if err != nil {
			return slots{}, err
		}
		if _, err := harness.Object(settings, "env", path); err != nil {
			return slots{}, err
		}
		layers = append(layers, settings)
	}
	return pinned(s, layers...), nil
}

// pinned is s with the pins of settings layers, lowest first. Claude Code assigns each layer's env over the last and
// runs a nonblank ANTHROPIC_MODEL over every layer's model, so a blank variable in a later layer clears an earlier one:
// ANTHROPIC_MODEL falls back to the model, and a slot to its value in s.
func pinned(s slots, layers ...map[string]any) slots {
	defaults := s
	var model, envModel string
	for _, settings := range layers {
		env, _ := settings["env"].(map[string]any)
		if v, _ := settings["model"].(string); bare(v) != "" {
			model = bare(v)
		}
		envModel = pin(env, "ANTHROPIC_MODEL", envModel, "")
		s.opus = pin(env, "ANTHROPIC_DEFAULT_OPUS_MODEL", s.opus, defaults.opus)
		s.sonnet = pin(env, "ANTHROPIC_DEFAULT_SONNET_MODEL", s.sonnet, defaults.sonnet)
		s.haiku = pin(env, "ANTHROPIC_DEFAULT_HAIKU_MODEL", s.haiku, defaults.haiku)
		s.fable = pin(env, "ANTHROPIC_DEFAULT_FABLE_MODEL", s.fable, defaults.fable)
		s.custom = pin(env, "ANTHROPIC_CUSTOM_MODEL_OPTION", s.custom, defaults.custom)
	}
	s.main = cmp.Or(envModel, model, s.main)
	return s
}

// pin returns the string set under key without its `[1m]` suffix, cleared when that is blank, and current when key is
// absent.
func pin(env map[string]any, key, current, cleared string) string {
	v, ok := env[key].(string)
	if !ok {
		return current
	}
	return cmp.Or(bare(v), cleared)
}
