// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package claude

import (
	"cmp"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

type layer struct {
	path     string
	settings map[string]any
}

func nativeLayerPaths(userHome, workDir string) []string {
	paths := []string{filepath.Join(userHome, ".claude", "settings.json")}
	if workDir != "" {
		paths = append(paths,
			filepath.Join(workDir, ".claude", "settings.json"),
			filepath.Join(workDir, ".claude", "settings.local.json"),
		)
	}
	return paths
}

// withPins adopts model pins from the dedicated settings cfg, then canonical user and project settings, local over
// shared. The overlay outranks native settings, so without this it would silently override them. Pins are not checked
// against the granted aliases.
func withPins(s slots, cfg map[string]any, userHome, workDir string) (slots, error) {
	layers := []layer{{settings: cfg}}
	for _, path := range nativeLayerPaths(userHome, workDir) {
		settings, err := harness.ReadObject(path)
		if err != nil {
			return slots{}, err
		}
		if _, err := harness.Object(settings, "env", path); err != nil {
			return slots{}, err
		}
		layers = append(layers, layer{path: path, settings: settings})
	}
	res, _ := pinnedFrom(s, layers...)
	return res, nil
}

// Effective returns the model alias that Launch will run across the dedicated, user and project settings layers,
// reading planned file contents from planned instead of disk, and the path of the layer whose model or ANTHROPIC_MODEL
// setting selected it.
func Effective(
	t []models.Model,
	userHome, workDir string,
	planned map[string][]byte,
) (path, alias string, err error) {
	if userHome == "" {
		return "", "", config.ErrNoHome
	}
	paths := append(
		[]string{filepath.Join(userHome, SettingsFile)},
		nativeLayerPaths(userHome, workDir)...)
	layers := make([]layer, 0, len(paths))
	for _, p := range paths {
		var settings map[string]any
		if data, ok := planned[p]; ok {
			if err := json.Unmarshal(data, &settings); err != nil {
				return "", "", err
			}
			if settings == nil {
				return "", "", fmt.Errorf("%s is not a JSON object; fix or move it, then retry", p)
			}
		} else {
			settings, err = harness.ReadObject(p)
			if err != nil {
				return "", "", err
			}
		}
		if _, err := harness.Object(settings, "env", p); err != nil {
			return "", "", err
		}
		layers = append(layers, layer{path: p, settings: settings})
	}
	s, selectedPath := pinnedFrom(defaultSlots(t), layers...)
	return selectedPath, cmp.Or(running(s, s.main, nil), s.main), nil
}

// pinned is s with the pins of settings layers, lowest first. Claude Code assigns each layer's env over the last and
// runs a nonblank ANTHROPIC_MODEL over every layer's model, so a blank variable in a later layer clears an earlier one:
// ANTHROPIC_MODEL falls back to the model, and a slot to its value in s.
func pinned(s slots, layers ...map[string]any) slots {
	named := make([]layer, len(layers))
	for i, settings := range layers {
		named[i] = layer{settings: settings}
	}
	res, _ := pinnedFrom(s, named...)
	return res
}

func pinnedFrom(s slots, layers ...layer) (slots, string) {
	defaults := s
	var model, modelPath, envModel, envModelPath string
	for _, l := range layers {
		env, _ := l.settings["env"].(map[string]any)
		if v, _ := l.settings["model"].(string); bare(v) != "" {
			model, modelPath = bare(v), l.path
		}
		if v, ok := env["ANTHROPIC_MODEL"].(string); ok {
			if b := bare(v); b != "" {
				envModel, envModelPath = b, l.path
			} else {
				envModel, envModelPath = "", ""
			}
		}
		s.opus = pin(env, "ANTHROPIC_DEFAULT_OPUS_MODEL", s.opus, defaults.opus)
		s.sonnet = pin(env, "ANTHROPIC_DEFAULT_SONNET_MODEL", s.sonnet, defaults.sonnet)
		s.haiku = pin(env, "ANTHROPIC_DEFAULT_HAIKU_MODEL", s.haiku, defaults.haiku)
		s.fable = pin(env, "ANTHROPIC_DEFAULT_FABLE_MODEL", s.fable, defaults.fable)
		s.custom = pin(env, "ANTHROPIC_CUSTOM_MODEL_OPTION", s.custom, defaults.custom)
	}
	s.main = cmp.Or(envModel, model, s.main)
	return s, cmp.Or(envModelPath, modelPath)
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
