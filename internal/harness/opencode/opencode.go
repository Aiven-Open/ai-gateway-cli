// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package opencode prepares an OpenCode launch against the gateway.
package opencode

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

type model struct {
	Name  string `json:"name"`
	Limit *limit `json:"limit,omitempty"`
}

type limit struct {
	Context int `json:"context"`
	Output  int `json:"output"`
}

// APIKey makes OpenCode read the key file of the gateway at baseURL when it loads its config. Unlike the other
// harnesses' helper it has no fallback to the environment.
func APIKey(baseURL string) string { return "{file:~/" + creds.Rel(baseURL) + "}" }

// Setup merges the aiven provider into the user's global config (configPath),
// leaving the user's default model alone unless in.Default asks to replace it.
func Setup(in harness.Input) (harness.Plan, error) {
	if in.UserHome == "" {
		return harness.Plan{}, config.ErrNoHome
	}
	path, other, err := configPath(in.UserHome, in.ConfigHome)
	if err != nil {
		return harness.Plan{}, err
	}
	src, cfg, err := harness.ReadJSON(path)
	if err != nil {
		return harness.Plan{}, err
	}
	stale, err := dropProvider(other)
	if err != nil {
		return harness.Plan{}, err
	}
	providers, err := harness.Object(cfg, "provider", path)
	if err != nil {
		return harness.Plan{}, err
	}
	list := map[string]model{}
	for _, m := range in.Table {
		e := model{Name: m.DisplayName()}
		if m.Context != nil && m.MaxOutput != nil {
			e.Limit = &limit{Context: *m.Context, Output: *m.MaxOutput}
		}
		list[m.Alias] = e
	}
	providers["aiven"] = map[string]any{
		"npm": "@ai-sdk/openai-compatible", "name": "Aiven AI Gateway",
		"options": map[string]string{"baseURL": in.BaseURL + "/v1", "apiKey": APIKey(in.BaseURL)},
		"models":  list,
	}
	if _, ok := cfg["$schema"]; !ok {
		cfg["$schema"] = "https://opencode.ai/config.json"
	}
	if in.Default {
		cfg["model"] = Model(in.Table)
	}
	data, err := harness.Edit(path, src, cfg)
	if err != nil {
		return harness.Plan{}, err
	}
	files := []harness.File{{Path: path, Data: data, Backup: true, Models: true, Reference: Reference}}
	if stale != nil {
		files = append(files, *stale)
	}
	return harness.Plan{Files: files}, nil
}

// dropProvider removes the aiven provider from other, the global file OpenCode reads first, if it declares one. OpenCode
// reads both files, so an old provider there would outlive setup's changes to the one it edits.
func dropProvider(other string) (*harness.File, error) {
	if other == "" {
		return nil, nil
	}
	src, cfg, err := harness.ReadJSON(other)
	if err != nil {
		return nil, err
	}
	providers, _ := cfg["provider"].(map[string]any)
	if _, ok := providers["aiven"]; !ok {
		return nil, nil
	}
	if delete(providers, "aiven"); len(providers) == 0 {
		delete(cfg, "provider")
	}
	data, err := harness.Edit(other, src, cfg)
	if err != nil {
		return nil, err
	}
	return &harness.File{Path: other, Data: data, Backup: true, Reference: Reference}, nil
}

// reference is OpenCode's {env:NAME} and {file:PATH} substitution, which it expands in any string of its config.
var reference = regexp.MustCompile(`^\{(?:env:[A-Za-z_][A-Za-z0-9_]*|file:[^{}\s]+)\}$`)

// Reference reports whether value is an OpenCode substitution, which names the variable or file holding a key.
func Reference(_, value string) bool { return reference.MatchString(value) }

// Model is the -m value that picks the gateway's default alias.
func Model(table []models.Model) string { return "aiven/" + models.SonnetOrFirst(table) }

// Launch selects the configured provider/model when there are no native args, passes native args unchanged, and
// leaves the native configuration tree untouched.
func Launch(in harness.Input) (harness.Plan, error) {
	args := harness.ModelArgs(in.Args, Model(in.Table), "-m")
	if len(args) != len(in.Args) {
		if in.UserHome == "" {
			return harness.Plan{}, config.ErrNoHome
		}
		path, _, err := configPath(in.UserHome, in.ConfigHome)
		if err != nil {
			return harness.Plan{}, err
		}
		cfg, err := harness.ReadObject(path)
		if err != nil {
			return harness.Plan{}, err
		}
		providers, _ := cfg["provider"].(map[string]any)
		p, _ := providers["aiven"].(map[string]any)
		list, _ := p["models"].(map[string]any)
		if list[models.SonnetOrFirst(in.Table)] == nil {
			return harness.Plan{}, errors.New("selected default is absent from the configured catalogue; refresh with setup")
		}
	}
	return harness.Plan{Argv: append([]string{"opencode"}, args...)}, nil
}

// Ready checks the file reference and protocol of the existing provider. It never needs the key in the environment,
// since it accepts only the {file:} reference.
func Ready(home, configHome, baseURL string) (bool, error) {
	// An empty home would make the path relative, naming a file in the working directory.
	if home == "" {
		return false, config.ErrNoHome
	}
	path, _, err := configPath(home, configHome)
	if err != nil {
		return false, err
	}
	cfg, err := harness.ReadObject(path)
	if err != nil {
		return false, err
	}
	providers, _ := cfg["provider"].(map[string]any)
	p, _ := providers["aiven"].(map[string]any)
	options, _ := p["options"].(map[string]any)
	list, _ := p["models"].(map[string]any)
	if p["npm"] != "@ai-sdk/openai-compatible" || options["baseURL"] != baseURL+"/v1" ||
		options["apiKey"] != APIKey(baseURL) || len(list) == 0 {
		return false, fmt.Errorf("%s has no compatible Aiven provider", path)
	}
	return false, nil
}

// configPath is the global config file OpenCode gives the last word, under XDG_CONFIG_HOME as OpenCode roots it: it
// reads opencode.json, then opencode.jsonc over it, and edits the last of the two that is a file. The second result
// is opencode.json when both are. Like OpenCode, it skips a directory or other special file; unlike it, it keeps a symlink
// for every configuration read to refuse.
func configPath(home, configHome string) (string, string, error) {
	root := filepath.Join(home, ".config")
	if configHome != "" {
		// OpenCode would resolve a relative one against the directory each session starts in.
		if !filepath.IsAbs(configHome) {
			return "", "", errors.New("XDG_CONFIG_HOME is not an absolute path, so OpenCode reads its config relative " +
				"to where it starts; set it to an absolute path or unset it, then retry")
		}
		root = configHome
	}
	dir := filepath.Join(root, "opencode")
	var files []string
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		file := filepath.Join(dir, name)
		if info, err := os.Lstat(file); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || info.Mode().IsRegular() {
				files = append(files, file)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("inspect %s: %w", file, err)
		}
	}
	switch len(files) {
	case 0:
		return filepath.Join(dir, "opencode.json"), "", nil
	case 1:
		return files[0], "", nil
	}
	return files[1], files[0], nil
}
