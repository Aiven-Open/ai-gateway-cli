// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package claude

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Aiven-Open/ai-gateway-cli/internal/models"
)

// withPins adopts the model choices in the project's Claude settings, since the overlay outranks them. A pin the key is
// not granted keeps the launcher's choice and yields a warning.
func withPins(s slots, t []models.Model, workDir string) (slots, []string) {
	env, model := projectPins(workDir)
	var warnings []string
	use := func(key, pin, current string) string {
		if pin == "" {
			return current
		}
		if _, ok := models.Find(t, pin); !ok {
			warnings = append(warnings, fmt.Sprintf("aiven-llm: project setting %s %q is not granted to your key; using %s\n", key, pin, current))
			return current
		}
		return pin
	}
	s.opus = use("ANTHROPIC_DEFAULT_OPUS_MODEL", env["ANTHROPIC_DEFAULT_OPUS_MODEL"], s.opus)
	s.sonnet = use("ANTHROPIC_DEFAULT_SONNET_MODEL", env["ANTHROPIC_DEFAULT_SONNET_MODEL"], s.sonnet)
	s.haiku = use("ANTHROPIC_DEFAULT_HAIKU_MODEL", env["ANTHROPIC_DEFAULT_HAIKU_MODEL"], s.haiku)
	s.fable = use("ANTHROPIC_DEFAULT_FABLE_MODEL", env["ANTHROPIC_DEFAULT_FABLE_MODEL"], s.fable)
	// Claude Code ranks ANTHROPIC_MODEL above the `model` setting and resolves its aliases through the slots.
	key, main := "ANTHROPIC_MODEL", env["ANTHROPIC_MODEL"]
	if main == "" {
		key, main = "model", model
	}
	switch main {
	case "", "default":
	case "opus":
		s.main = s.opus
	case "sonnet":
		s.main = s.sonnet
	case "haiku":
		s.main = s.haiku
	case "fable":
		s.main = s.fable
	default:
		s.main = use(key, main, s.main)
	}
	return s, warnings
}

// projectPins reads the env and top-level `model` of the project's settings, local over shared per key, `[1m]` stripped.
// Unreadable or malformed files, non-string and blank values count as unset.
func projectPins(workDir string) (map[string]string, string) {
	env, model := map[string]string{}, ""
	for _, name := range []string{"settings.json", "settings.local.json"} {
		var settings struct {
			Model any            `json:"model"`
			Env   map[string]any `json:"env"`
		}
		data, err := os.ReadFile(filepath.Join(workDir, ".claude", name))
		if err != nil || json.Unmarshal(data, &settings) != nil {
			continue
		}
		model = cmp.Or(pinValue(settings.Model), model)
		for k, v := range settings.Env {
			env[k] = cmp.Or(pinValue(v), env[k])
		}
	}
	return env, model
}

func pinValue(v any) string {
	s, _ := v.(string)
	return strings.TrimSuffix(s, suffix)
}
