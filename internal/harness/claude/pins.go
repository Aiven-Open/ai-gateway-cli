// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package claude

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

type layerKind int

const (
	layerDedicated layerKind = iota
	layerUser
	layerProject
)

type layerSpec struct {
	kind     layerKind
	path     string
	settings map[string]any
}

type layer struct {
	kind     layerKind
	path     string
	settings map[string]any
}

// sameDir reports whether a and b refer to the same directory, resolving symlinks when present.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if ca == cb {
		return true
	}
	ia, errA := os.Stat(ca)
	ib, errB := os.Stat(cb)
	if errA == nil && errB == nil && os.SameFile(ia, ib) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(ca)
	rb, errB := filepath.EvalSymlinks(cb)
	return errA == nil && errB == nil && ra == rb
}

// projectLayerPaths returns the project-level .claude settings files under workDir, excluding
// <workDir>/.claude/settings.json when workDir is userHome so ~/.claude/settings.json is never
// treated as an unrepairable project file or folded twice.
func projectLayerPaths(userHome, workDir string) []string {
	if workDir == "" {
		return nil
	}
	if sameDir(userHome, workDir) {
		return []string{filepath.Join(workDir, ".claude", "settings.local.json")}
	}
	return []string{
		filepath.Join(workDir, ".claude", "settings.json"),
		filepath.Join(workDir, ".claude", "settings.local.json"),
	}
}

func nativeLayerSpecs(userHome, workDir string) []layerSpec {
	specs := []layerSpec{{kind: layerUser, path: filepath.Join(userHome, ".claude", "settings.json")}}
	for _, p := range projectLayerPaths(userHome, workDir) {
		specs = append(specs, layerSpec{kind: layerProject, path: p})
	}
	return specs
}

func loadLayers(specs []layerSpec, planned map[string][]byte) ([]layer, error) {
	layers := make([]layer, 0, len(specs))
	for _, sp := range specs {
		settings := sp.settings
		if settings == nil {
			if data, ok := planned[sp.path]; ok {
				if err := json.Unmarshal(data, &settings); err != nil {
					return nil, err
				}
				if settings == nil {
					return nil, fmt.Errorf("%s is not a JSON object; fix or move it, then retry", sp.path)
				}
			} else {
				var err error
				settings, err = harness.ReadObject(sp.path)
				if err != nil {
					return nil, err
				}
			}
		}
		if _, err := harness.Object(settings, "env", sp.path); err != nil {
			return nil, err
		}
		layers = append(layers, layer{kind: sp.kind, path: sp.path, settings: settings})
	}
	return layers, nil
}

// layerSelectsModel reports whether settings explicitly sets model or env.ANTHROPIC_MODEL.
func layerSelectsModel(settings map[string]any) bool {
	if len(settings) == 0 {
		return false
	}
	env, _ := settings["env"].(map[string]any)
	if v, ok := env["ANTHROPIC_MODEL"].(string); ok && bare(v) != "" {
		return true
	}
	v, _ := settings["model"].(string)
	return bare(v) != ""
}

// layerPinsTier reports whether settings sets a non-blank tier slot variable.
func layerPinsTier(settings map[string]any) bool {
	env, _ := settings["env"].(map[string]any)
	for _, v := range tierVars {
		if s, ok := env[v].(string); ok && bare(s) != "" {
			return true
		}
	}
	return false
}

// layerHasCustomOption reports whether settings sets a non-blank env.ANTHROPIC_CUSTOM_MODEL_OPTION.
func layerHasCustomOption(settings map[string]any) bool {
	if len(settings) == 0 {
		return false
	}
	env, _ := settings["env"].(map[string]any)
	v, ok := env["ANTHROPIC_CUSTOM_MODEL_OPTION"].(string)
	return ok && bare(v) != ""
}

// ProjectUngranted reports whether project .claude/settings*.json layers independently select an ungranted model
// that setup cannot repair, even if a dedicated or user-level env pin would otherwise mask it.
func ProjectUngranted(
	t []models.Model,
	userHome, workDir string,
) (path, alias, setting string, ok bool) {
	if len(t) == 0 || workDir == "" {
		return "", "", "", false
	}
	paths := projectLayerPaths(userHome, workDir)
	specs := make([]layerSpec, len(paths))
	for i, p := range paths {
		specs[i] = layerSpec{kind: layerProject, path: p}
	}
	layers, err := loadLayers(specs, nil)
	if err != nil {
		return "", "", "", false
	}
	s, selectedPath, selectedSetting := pinnedFrom(t, defaultSlots(t), layers...)
	if selectedPath == "" {
		return "", "", "", false
	}
	eff := cmp.Or(running(s, s.main, nil), s.main)
	if _, granted := models.Find(t, eff); granted {
		return "", "", "", false
	}
	return selectedPath, eff, selectedSetting, true
}

// withPins adopts model pins from the dedicated settings cfg, then canonical user and project settings, local over
// shared. When cfg holds an explicit launcher-only dedicated choice (granted model with blank env.ANTHROPIC_MODEL,
// or granted env.ANTHROPIC_MODEL), user ~/.claude/settings.json pins do not override it; project layers always win.
func withPins(
	t []models.Model,
	s slots,
	cfg map[string]any,
	userHome, workDir string,
) (slots, error) {
	specs := append(
		[]layerSpec{{kind: layerDedicated, settings: cfg}},
		nativeLayerSpecs(userHome, workDir)...,
	)
	layers, err := loadLayers(specs, nil)
	if err != nil {
		return slots{}, err
	}
	res, _, _ := pinnedFrom(t, s, layers...)
	return res, nil
}

// Effective returns the model alias that Launch will run across the dedicated, user and project settings layers,
// reading planned file contents from planned instead of disk, together with the selecting layer's path and setting
// name ("env.ANTHROPIC_MODEL", "model", or "").
func Effective(
	t []models.Model,
	userHome, workDir string,
	planned map[string][]byte,
) (path, alias, setting string, err error) {
	if userHome == "" {
		return "", "", "", config.ErrNoHome
	}
	specs := append(
		[]layerSpec{{kind: layerDedicated, path: filepath.Join(userHome, SettingsFile)}},
		nativeLayerSpecs(userHome, workDir)...,
	)
	layers, err := loadLayers(specs, planned)
	if err != nil {
		return "", "", "", err
	}
	s, selectedPath, selectedSetting := pinnedFrom(t, defaultSlots(t), layers...)
	return selectedPath, cmp.Or(running(s, s.main, nil), s.main), selectedSetting, nil
}

// pinned is s with the pins of settings layers, lowest first. Claude Code assigns each layer's env over the last and
// runs a nonblank ANTHROPIC_MODEL over every layer's model, so a blank variable in a later layer clears an earlier one:
// ANTHROPIC_MODEL falls back to the model, and a slot to its value in s.
func pinned(s slots, layers ...map[string]any) slots {
	named := make([]layer, len(layers))
	for i, settings := range layers {
		named[i] = layer{kind: layerProject, settings: settings}
	}
	res, _, _ := pinnedFrom(nil, s, named...)
	return res
}

func pinnedFrom(t []models.Model, s slots, layers ...layer) (slots, string, string) {
	defaults := s
	var model, modelPath, envModel, envModelPath string
	dedicatedOverridesUser := false
	dedicatedClearsUserCustom := false
	for _, l := range layers {
		env, _ := l.settings["env"].(map[string]any)
		if l.kind == layerDedicated {
			if len(t) > 0 {
				if v, ok := env["ANTHROPIC_MODEL"].(string); ok && bare(v) == "" {
					if m, _ := l.settings["model"].(string); bare(m) != "" {
						if _, granted := models.Find(t, bare(m)); granted {
							dedicatedOverridesUser = true
						}
					}
				}
			}
			if v, ok := env["ANTHROPIC_CUSTOM_MODEL_OPTION"].(string); ok && bare(v) == "" {
				dedicatedClearsUserCustom = true
			}
		}
		if l.kind == layerUser && dedicatedOverridesUser {
			// Explicit launcher-only choice in aiven.settings.json already holds the resolved model,
			// tier slots (preserving granted Claude tiers on custom picks) and custom option.
			continue
		}
		if v, _ := l.settings["model"].(string); bare(v) != "" {
			model, modelPath = defaultAlias(bare(v), defaults), l.path
		}
		if v, ok := env["ANTHROPIC_MODEL"].(string); ok {
			if b := bare(v); b != "" {
				envModel, envModelPath = defaultAlias(b, defaults), l.path
			} else {
				envModel, envModelPath = "", ""
			}
		}
		s.opus = pin(env, "ANTHROPIC_DEFAULT_OPUS_MODEL", s.opus, defaults.opus)
		s.sonnet = pin(env, "ANTHROPIC_DEFAULT_SONNET_MODEL", s.sonnet, defaults.sonnet)
		s.haiku = pin(env, "ANTHROPIC_DEFAULT_HAIKU_MODEL", s.haiku, defaults.haiku)
		s.fable = pin(env, "ANTHROPIC_DEFAULT_FABLE_MODEL", s.fable, defaults.fable)
		if l.kind != layerUser || !dedicatedClearsUserCustom {
			s.custom = pin(env, "ANTHROPIC_CUSTOM_MODEL_OPTION", s.custom, defaults.custom)
		}
	}
	s.main = cmp.Or(envModel, model, s.main)
	if envModel != "" {
		return s, envModelPath, "env.ANTHROPIC_MODEL"
	}
	if model != "" {
		return s, modelPath, "model"
	}
	return s, "", ""
}

// defaultAlias is the main model of defaults when model is Claude Code's default alias, else model.
func defaultAlias(model string, defaults slots) string {
	if id, ok := tierSlot(defaults, model); ok && strings.EqualFold(model, "default") {
		return id
	}
	return model
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
