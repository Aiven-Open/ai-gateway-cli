// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package codex prepares a Codex CLI launch against the gateway.
package codex

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/Aiven-Open/ai-gateway-cli/internal/harness"
	"github.com/Aiven-Open/ai-gateway-cli/internal/models"
)

const providerID = "aiven-gateway"

type config struct {
	Model              string              `toml:"model"`
	ModelProvider      string              `toml:"model_provider"`
	ModelProviders     map[string]provider `toml:"model_providers"`
	ModelContextWindow int                 `toml:"model_context_window,omitempty"`
	// ReviewModel is pinned too: an inherited one need not be granted.
	ReviewModel string `toml:"review_model"`
}

type provider struct {
	Name    string `toml:"name"`
	BaseURL string `toml:"base_url"`
	EnvKey  string `toml:"env_key"`
	WireAPI string `toml:"wire_api"`
}

// Prepare adds the gateway provider to the user's own Codex as `-c` overrides, which Codex ranks above
// config.toml and below `-m`; Isolated renders a private CODEX_HOME instead. The provider reads the key from the environment.
func Prepare(in harness.Input) (harness.Plan, error) {
	cfg := config{
		Model:         cmp.Or(models.FirstGranted(in.Table, "gpt-6.1-sol", "gpt-6-sol"), models.DefaultAlias(in.Table, "gpt-")),
		ModelProvider: providerID,
		ModelProviders: map[string]provider{providerID: {
			Name: "Aiven LLM Gateway", BaseURL: in.BaseURL + "/v1", EnvKey: "AIVEN_GATEWAY_KEY", WireAPI: "responses",
		}},
	}
	cfg.ReviewModel = cfg.Model
	if m, ok := models.Find(in.Table, effectiveModel(in.Args, cfg.Model)); ok && m.Context != nil {
		cfg.ModelContextWindow = *m.Context
	}
	env := []string{"AIVEN_GATEWAY_KEY=" + in.Key}
	if in.Isolated {
		data, err := toml.Marshal(cfg)
		if err != nil {
			return harness.Plan{}, err
		}
		home := filepath.Join(in.Home, "codex")
		return harness.Plan{
			Files:  []harness.File{{Path: filepath.Join(home, "config.toml"), Data: data}},
			Argv:   append([]string{"codex"}, in.Args...),
			Env:    append(env, "CODEX_HOME="+home),
			Notice: models.Table(in.Table),
		}, nil
	}
	if in.ProfileDir == "" {
		return harness.Plan{}, errors.New("cannot locate your Codex profile (HOME unset); run: aiven-llm codex --isolated")
	}
	files, err := profileFiles(in.ProfileDir)
	if err != nil {
		return harness.Plan{}, err
	}
	for _, own := range files {
		if definesProvider(own) {
			return harness.Plan{}, fmt.Errorf("%s defines model provider %s, which Codex would merge into the gateway's. Remove it, or run: aiven-llm codex --isolated", own, providerID)
		}
	}
	argv, err := overrides(cfg)
	if err != nil {
		return harness.Plan{}, err
	}
	return harness.Plan{
		Argv:   append(append([]string{"codex"}, argv...), in.Args...),
		Env:    env,
		Notice: models.Table(in.Table),
	}, nil
}

// effectiveModel is the model the user's args select over def, since Codex applies model_context_window to whichever
// model runs; "" when they set it through `-c model=`, which this does not parse.
func effectiveModel(args []string, def string) string {
	model := def
	for i, a := range args {
		switch {
		case (a == "-m" || a == "--model") && i+1 < len(args):
			model = args[i+1]
		case strings.HasPrefix(a, "--model="):
			model = strings.TrimPrefix(a, "--model=")
		case (a == "-c" || a == "--config") && i+1 < len(args) && setsModel(args[i+1]):
			return ""
		case strings.HasPrefix(a, "--config=") && setsModel(strings.TrimPrefix(a, "--config=")):
			return ""
		}
	}
	return model
}

func setsModel(override string) bool {
	k, _, _ := strings.Cut(override, "=")
	return strings.TrimSpace(k) == "model"
}

// profileFiles are the config files Codex can merge under the `-c` overrides: config.toml and every profile's
// `<name>.config.toml` (Codex 0.160.0), checked whether or not this launch selects one, so no `-p` spelling slips past.
// An unlistable dir fails: Codex may still open a profile in it by name.
func profileFiles(dir string) ([]string, error) {
	files := []string{filepath.Join(dir, "config.toml")}
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("cannot list your Codex profile %s: %w; run: aiven-llm codex --isolated", dir, err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".config.toml") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	return files, nil
}

// definesProvider reports whether the Codex config.toml at path has its own table for the gateway provider.
func definesProvider(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var c struct {
		ModelProviders map[string]any `toml:"model_providers"`
	}
	_ = toml.Unmarshal(data, &c) // a file Codex cannot parse is Codex's error to report
	_, ok := c.ModelProviders[providerID]
	return ok
}

// overrides renders cfg as `-c key=value` pairs, the provider as a dotted key so the user's own providers stay.
func overrides(cfg config) ([]string, error) {
	pairs := [][2]any{
		{"model", cfg.Model},
		{"review_model", cfg.ReviewModel},
		{"model_provider", cfg.ModelProvider},
		{"model_providers." + providerID, cfg.ModelProviders[providerID]},
	}
	var argv []string
	for _, p := range pairs {
		v, err := tomlValue(p[1])
		if err != nil {
			return nil, err
		}
		argv = append(argv, "-c", p[0].(string)+"="+v)
	}
	if cfg.ModelContextWindow != 0 {
		argv = append(argv, "-c", "model_context_window="+strconv.Itoa(cfg.ModelContextWindow))
	}
	return argv, nil
}

// tomlValue renders v as one TOML value, tables inline.
func tomlValue(v any) (string, error) {
	data, err := toml.Marshal(struct {
		V any `toml:"v,inline"`
	}{v})
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimPrefix(string(data), "v = "), "\n"), nil
}
