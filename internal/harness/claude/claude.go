// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package claude prepares a Claude Code launch against the gateway.
package claude

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Aiven-Open/ai-gateway-cli/internal/harness"
	"github.com/Aiven-Open/ai-gateway-cli/internal/models"
)

// Claude Code assumes 200K for model ids it does not know; the `[1m]` suffix declares 1M and is stripped before the request.
const (
	mainFloor = 200001
	subFloor  = 1_000_000
	suffix    = "[1m]"
)

// Input is everything Prepare needs; the table is non-empty.
type Input struct {
	Table   []models.Model
	Key     string
	BaseURL string
	Home    string
	// WorkDir is where project `.claude/settings*.json` pins are read from.
	WorkDir  string
	Isolated bool
	// Args are forwarded to claude after `--settings`.
	Args []string
}

// slots are the aliases chosen for each Claude model slot, before the `[1m]` suffix.
type slots struct{ main, opus, sonnet, haiku, fable, custom string }

// Prepare picks the alias for each Claude slot and renders the settings overlay and process environment.
func Prepare(in Input) harness.Plan {
	t := in.Table
	s, warnings := withPins(defaultSlots(t), t, in.WorkDir)
	main := withSuffix(t, s.main, mainFloor)
	// A pool behind the main model can be smaller than 1M, so compact before its limit.
	window := ""
	if main != s.main {
		if m, _ := models.Find(t, s.main); *m.Context < subFloor {
			window = strconv.Itoa(*m.Context)
		}
	}
	ids := map[string]string{
		"OPUS":   withSuffix(t, s.opus, subFloor),
		"SONNET": withSuffix(t, s.sonnet, subFloor),
		"HAIKU":  withSuffix(t, s.haiku, subFloor),
		"FABLE":  withSuffix(t, s.fable, subFloor),
		"CUSTOM": withSuffix(t, s.custom, subFloor),
	}

	overlay := OverlayPath(in.Home, in.WorkDir)
	var env []string
	if in.Isolated {
		env = append(env, "CLAUDE_CONFIG_DIR="+filepath.Join(in.Home, "claude"))
	}
	if window != "" {
		env = append(env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW="+window)
	}
	env = append(env,
		"ANTHROPIC_BASE_URL="+in.BaseURL,
		"ANTHROPIC_API_KEY=",
		"CLAUDE_CODE_USE_BEDROCK=",
		"CLAUDE_CODE_USE_VERTEX=",
		"CLAUDE_CODE_USE_FOUNDRY=",
		"ANTHROPIC_MODEL="+main,
	)
	env = append(env, slot(t, "ANTHROPIC_DEFAULT_OPUS_MODEL", s.opus, ids["OPUS"])...)
	env = append(env, slot(t, "ANTHROPIC_DEFAULT_SONNET_MODEL", s.sonnet, ids["SONNET"])...)
	env = append(env, slot(t, "ANTHROPIC_DEFAULT_HAIKU_MODEL", s.haiku, ids["HAIKU"])...)
	env = append(env, slot(t, "ANTHROPIC_DEFAULT_FABLE_MODEL", s.fable, ids["FABLE"])...)
	env = append(env, slot(t, "ANTHROPIC_CUSTOM_MODEL_OPTION", s.custom, ids["CUSTOM"])...)
	env = append(env, "ANTHROPIC_AUTH_TOKEN="+in.Key)
	return harness.Plan{
		Files:  []harness.File{{Path: overlay, Data: overlayJSON(in.BaseURL, main, ids, window)}},
		Argv:   append([]string{"claude", "--settings", overlay}, in.Args...),
		Env:    env,
		Notice: strings.Join(warnings, ""),
	}
}

// defaultSlots walks the preference ladders, production ids first, then the POC gateway's aliases; an empty tier takes the main model.
func defaultSlots(t []models.Model) slots {
	s := slots{
		main:   models.FirstGranted(t, "claude-sonnet-5-5", "deepseek-flash", "glm-fast", "glm", "claude-opus"),
		opus:   models.FirstGranted(t, "claude-opus-5-5", "claude-opus", "claude-fable", "claude-sonnet"),
		sonnet: models.FirstGranted(t, "claude-sonnet-5-5", "glm-fast", "glm", "qwen", "deepseek", "claude-sonnet"),
		// `claude-haiku-5-5` is Haiku 5.5's newer name; the dotted id is today's.
		haiku: models.FirstGranted(t, "claude-haiku-5-5", "claude-haiku-5.5", "claude-haiku-4-5", "gemini-flash", "gpt-luna", "claude-haiku", "glm-fast"),
		// ai.aiven.io serves no Fable, so the slot takes Opus rather than the dearest GPT.
		fable:  models.FirstGranted(t, "claude-opus-5-5", "gpt-astra", "gpt-terra", "claude-fable"),
		custom: models.FirstGranted(t, "gpt-6.1-sol", "gpt-6-sol", "gpt-sol", "gpt-terra", "gpt-luna"),
	}
	if s.custom == "" {
		s.custom = models.FirstNotIn(t, s.opus, s.sonnet, s.haiku, s.fable)
	}
	s.main = cmp.Or(s.main, models.FirstAlias(t))
	// A blank Fable slot would send Claude Code's built-in Fable id, which the gateway does not serve.
	s.opus, s.sonnet, s.haiku, s.fable = cmp.Or(s.opus, s.main), cmp.Or(s.sonnet, s.main), cmp.Or(s.haiku, s.main), cmp.Or(s.fable, s.main)
	return s
}

// OverlayPath is the `--settings` file for workDir: it holds that project's pins, so launches in other projects must not rewrite it.
func OverlayPath(home, workDir string) string {
	sum := sha256.Sum256([]byte(workDir))
	return filepath.Join(home, "claude-settings-"+hex.EncodeToString(sum[:4])+".json")
}

// overlayJSON is the `--settings` file. It outranks project settings; it never holds the key.
func overlayJSON(baseURL, main string, ids map[string]string, window string) []byte {
	env := map[string]string{
		"ANTHROPIC_BASE_URL":             baseURL,
		"ANTHROPIC_MODEL":                main,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   ids["OPUS"],
		"ANTHROPIC_DEFAULT_SONNET_MODEL": ids["SONNET"],
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  ids["HAIKU"],
		"ANTHROPIC_DEFAULT_FABLE_MODEL":  ids["FABLE"],
		"ANTHROPIC_CUSTOM_MODEL_OPTION":  ids["CUSTOM"],
		"ANTHROPIC_API_KEY":              "",
		"CLAUDE_CODE_USE_BEDROCK":        "",
		"CLAUDE_CODE_USE_VERTEX":         "",
		"CLAUDE_CODE_USE_FOUNDRY":        "",
		"AWS_BEARER_TOKEN_BEDROCK":       "",
	}
	if window != "" {
		env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = window
	}
	return harness.JSON(map[string]any{"model": main, "env": env})
}

// slot renders `<prefix>`, `<prefix>_NAME` and `<prefix>_DESCRIPTION`; the last two are blank without an alias.
func slot(t []models.Model, prefix, alias, id string) []string {
	name, desc := "", ""
	if alias != "" {
		m, _ := models.Find(t, alias)
		m.Alias = alias
		name, desc = alias+" (gateway)", m.Description()
	}
	return []string{prefix + "=" + id, prefix + "_NAME=" + name, prefix + "_DESCRIPTION=" + desc}
}

func withSuffix(t []models.Model, alias string, floor int) string {
	if m, ok := models.Find(t, alias); ok && m.Context != nil && *m.Context >= floor {
		return alias + suffix
	}
	return alias
}
