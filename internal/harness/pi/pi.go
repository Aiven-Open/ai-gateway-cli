// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package pi prepares a Pi launch against the gateway.
package pi

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

// APIKey makes pi run the key helper of the gateway at baseURL: pi runs a value starting with ! through /bin/sh, or on
// Windows through the bash WindowsShell finds, and uses its output.
func APIKey(baseURL string) string { return "!sh -c '" + creds.Helper(baseURL, runtime.GOOS) + "'" }

type model struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	API           string   `json:"api"`
	BaseURL       string   `json:"baseUrl"`
	Reasoning     bool     `json:"reasoning"`
	Input         []string `json:"input"`
	ContextWindow *int     `json:"contextWindow,omitempty"`
	MaxTokens     *int     `json:"maxTokens,omitempty"`
	Cost          *cost    `json:"cost,omitempty"`
}

// cost is USD per million tokens; Pi has no cache pricing to learn from the gateway.
type cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// SavedCredential refuses a credential saved for the aiven provider in Pi's auth.json. Pi sends it instead of the key
// command in models.json and has no setting that reverses this, so the key setup checks would not be the one Pi sends.
// The file is read, never changed: it holds the user's other providers' credentials too.
func SavedCredential(home string) error {
	path := filepath.Join(home, ".pi", "agent", "auth.json")
	auth, err := harness.ReadObject(path)
	if err != nil {
		return err
	}
	if _, ok := auth["aiven"]; ok {
		return fmt.Errorf("%s holds a credential saved for aiven, which Pi sends instead of the gateway key; run /logout in pi and choose aiven, then retry", path)
	}
	return nil
}

// Setup merges the aiven provider into the user's ~/.pi/agent/models.json, leaving the user's default in settings.json
// alone unless in.Default asks to replace it. The provider reads the gateway's key file.
func Setup(in harness.Input) (harness.Plan, error) {
	if in.UserHome == "" {
		return harness.Plan{}, config.ErrNoHome
	}
	if err := SavedCredential(in.UserHome); err != nil {
		return harness.Plan{}, err
	}
	path := filepath.Join(in.UserHome, ".pi", "agent", "models.json")
	src, cfg, err := harness.ReadJSON(path)
	if err != nil {
		return harness.Plan{}, err
	}
	providers, err := harness.Object(cfg, "providers", path)
	if err != nil {
		return harness.Plan{}, err
	}
	settingsPath := filepath.Join(in.UserHome, ".pi", "agent", "settings.json")
	settingsSrc, settings, err := harness.ReadJSON(settingsPath)
	if err != nil {
		return harness.Plan{}, err
	}
	var list []model
	for _, m := range models.Sorted(in.Table) {
		e := model{ID: m.Alias, Name: m.DisplayName(), Reasoning: true, Input: []string{"text", "image"}, ContextWindow: m.Context, MaxTokens: m.MaxOutput}
		protocol := models.ModelProtocol(m.Alias)
		e.API, e.BaseURL = string(protocol), protocol.BaseURL(in.BaseURL)
		if m.Priced() {
			e.Cost = &cost{Input: *m.InUSD1M, Output: *m.OutUSD1M}
		}
		list = append(list, e)
	}
	providers["aiven"] = map[string]any{
		"baseUrl": in.BaseURL + "/v1", "api": "openai-completions", "apiKey": APIKey(in.BaseURL), "models": list,
	}
	// Pi skips // comments and trailing commas in models.json but not block comments, and reads settings.json as plain
	// JSON; it ignores a file it cannot parse, so what it would reject goes.
	data, err := harness.Edit(path, harness.Strip(src, harness.BlockComments), cfg)
	if err != nil {
		return harness.Plan{}, err
	}
	files := []harness.File{{Path: path, Data: data, Backup: true, Models: true}}
	if in.Default {
		settings["defaultProvider"], settings["defaultModel"] = "aiven", models.SonnetOrFirst(in.Table)
		if data, err = harness.Edit(settingsPath, harness.Strip(settingsSrc, harness.Strict), settings); err != nil {
			return harness.Plan{}, err
		}
		files = append(files, harness.File{Path: settingsPath, Data: data, Backup: true})
	}
	return harness.Plan{Files: files}, nil
}

// Model is the --model value that picks the gateway's default alias.
func Model(table []models.Model) string { return "aiven/" + models.SonnetOrFirst(table) }

// Launch selects the gateway default model in an already configured provider when there are no native args, and
// passes native args unchanged.
func Launch(in harness.Input) (harness.Plan, error) {
	args := harness.ModelArgs(in.Args, Model(in.Table), "--model")
	if len(args) != len(in.Args) {
		if in.UserHome == "" {
			return harness.Plan{}, config.ErrNoHome
		}
		cfg, err := harness.ReadObject(filepath.Join(in.UserHome, ".pi", "agent", "models.json"))
		if err != nil {
			return harness.Plan{}, err
		}
		providers, _ := cfg["providers"].(map[string]any)
		p, _ := providers["aiven"].(map[string]any)
		list, _ := p["models"].([]any)
		found := false
		for _, entry := range list {
			m, _ := entry.(map[string]any)
			found = found || m["id"] == models.SonnetOrFirst(in.Table)
		}
		if !found {
			return harness.Plan{}, errors.New("selected default is absent from the configured catalogue; refresh with setup")
		}
	}
	return harness.Plan{
		Argv: append([]string{"pi"}, args...),
	}, nil
}

// Ready checks existing wiring and reports whether a manual provider needs the key in its environment.
func Ready(home, baseURL string) (bool, error) {
	// An empty home would make the paths relative, naming files in the working directory.
	if home == "" {
		return false, config.ErrNoHome
	}
	if _, err := harness.ReadObject(filepath.Join(home, ".pi", "agent", "settings.json")); err != nil {
		return false, err
	}
	path := filepath.Join(home, ".pi", "agent", "models.json")
	cfg, err := harness.ReadObject(path)
	if err != nil {
		return false, err
	}
	providers, _ := cfg["providers"].(map[string]any)
	p, _ := providers["aiven"].(map[string]any)
	list, _ := p["models"].([]any)
	envKey := p["apiKey"] == "$"+creds.EnvVar || p["apiKey"] == "${"+creds.EnvVar+"}"
	if p["apiKey"] != APIKey(baseURL) && !envKey || len(list) == 0 {
		return false, fmt.Errorf("%s has no compatible Aiven provider", path)
	}
	for _, entry := range list {
		m, _ := entry.(map[string]any)
		id, _ := m["id"].(string)
		protocol := models.ModelProtocol(id)
		api, origin := string(protocol), protocol.BaseURL(baseURL)
		effectiveAPI, effectiveOrigin := m["api"], m["baseUrl"]
		if effectiveAPI == nil {
			effectiveAPI = p["api"]
		}
		if effectiveOrigin == nil {
			effectiveOrigin = p["baseUrl"]
		}
		if id == "" || effectiveOrigin != origin || effectiveAPI != api {
			return false, fmt.Errorf("%s has incompatible Aiven model wiring", path)
		}
	}
	return envKey, nil
}

// WindowsShell is the bash Pi runs a key command in on Windows, found the way Pi 1.5 finds it: Git Bash under
// ProgramFiles, then ProgramFiles(x86), then the first bash.exe on PATH. It is an error when there is none, or when that
// is the legacy WSL launcher under SystemRoot, which runs the command inside Linux, where USERPROFILE is not set: Pi
// would then fall back to cmd.exe or WSL, and neither runs the helper. Names in environ match ignoring case, as on
// Windows.
func WindowsShell(environ []string, exists func(string) bool, lookPath func(string) (string, error)) (string, error) {
	get := func(name string) string {
		var v string
		for _, kv := range environ {
			if n, value, _ := strings.Cut(kv, "="); strings.EqualFold(n, name) {
				v = value
			}
		}
		return v
	}
	for _, dir := range []string{get("ProgramFiles"), get("ProgramFiles(x86)")} {
		if path := dir + `\Git\bin\bash.exe`; dir != "" && exists(path) {
			return path, nil
		}
	}
	path, err := lookPath("bash.exe")
	if err != nil {
		return "", errors.New("pi runs its key helper in Git Bash on Windows, and none was found; install Git for Windows, then rerun")
	}
	root := strings.TrimRight(get("SystemRoot"), `\/`)
	legacy := root == "" && legacyWSL.MatchString(path)
	for _, dir := range []string{"System32", "Sysnative"} {
		legacy = legacy || root != "" && strings.EqualFold(strings.ReplaceAll(path, "/", `\`), root+`\`+dir+`\bash.exe`)
	}
	if legacy {
		return "", fmt.Errorf("pi would run its key helper in WSL through %s rather than Git Bash; install Git for Windows, then rerun", path)
	}
	return path, nil
}

var legacyWSL = regexp.MustCompile(`(?i)^[a-z]:[\\/]windows[\\/](system32|sysnative)[\\/]bash\.exe$`)
