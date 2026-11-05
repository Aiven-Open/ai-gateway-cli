// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package opencode_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/opencode"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

const (
	baseURL = "https://gateway.example"
	key     = "sk-test-valid"
)

func ptr[T any](v T) *T { return &v }

// granted holds a model with both limits, one with only a window, one with only an output limit, and one with neither.
func granted() []models.Model {
	return []models.Model{
		{Alias: "glm"},
		{Alias: "gpt-sol", Name: "GPT Sol", Context: ptr(400_000)},
		{Alias: "kimi", Name: "Kimi", MaxOutput: ptr(32_000)},
		{Alias: "claude-sonnet", Name: "Claude Sonnet", Context: ptr(1_000_000), MaxOutput: ptr(64_000)},
	}
}

func input(t *testing.T) harness.Input {
	t.Helper()
	return harness.Input{Table: granted(), BaseURL: baseURL, UserHome: t.TempDir()}
}

func configPath(in harness.Input) string {
	if in.ConfigHome != "" {
		return filepath.Join(in.ConfigHome, "opencode", "opencode.json")
	}
	return filepath.Join(in.UserHome, ".config", "opencode", "opencode.json")
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// decode reads f once written; the user's comments stay in it, so it is JSONC.
func decode(t *testing.T, f harness.File) map[string]any {
	t.Helper()
	obj, err := harness.ReadObject(f.Path)
	if err != nil {
		t.Fatalf("%s: %v", f.Path, err)
	}
	var v map[string]any
	if err := json.Unmarshal(harness.JSON(obj), &v); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(f.Data), key) {
		t.Errorf("%s holds the key:\n%s", f.Path, f.Data)
	}
	return v
}

// setUp runs Setup and writes its file, as setup does after its preview.
func setUp(t *testing.T, in harness.Input) map[string]any {
	t.Helper()
	plan, err := opencode.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 || plan.Files[0].Path != configPath(in) || !plan.Files[0].Backup || !plan.Files[0].Models ||
		plan.Files[0].Reference == nil || plan.Argv != nil || plan.Env != nil {
		t.Fatalf("plan = %+v", plan)
	}
	write(t, plan.Files[0].Path, string(plan.Files[0].Data))
	return decode(t, plan.Files[0])
}

func TestSetupAddsTheProviderAndLeavesTheDefaultAlone(t *testing.T) {
	want := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{"aiven": map[string]any{
			"npm": "@ai-sdk/openai-compatible", "name": "Aiven AI Gateway",
			"options": map[string]any{"baseURL": baseURL + "/v1", "apiKey": opencode.APIKey(baseURL)},
			"models": map[string]any{
				"glm":           map[string]any{"name": "glm"},
				"gpt-sol":       map[string]any{"name": "GPT Sol"},
				"kimi":          map[string]any{"name": "Kimi"},
				"claude-sonnet": map[string]any{"name": "Claude Sonnet", "limit": map[string]any{"context": 1e6, "output": 64000.0}},
			},
		}},
	}
	if got := setUp(t, input(t)); !reflect.DeepEqual(got, want) {
		t.Errorf("opencode.json = %v\nwant %v", got, want)
	}
}

func TestSetupKeepsTheUsersConfigUnlessAskedForTheDefault(t *testing.T) {
	in := input(t)
	write(t, configPath(in), `{
  // the user's own
  "$schema": "https://example.test/schema.json",
  "model": "local/qwen",
  "provider": {"local": {"npm": "x"}, "aiven": {"models": {"old": {}}, "x": 1}},
  "theme": "dark",
}`)
	got := setUp(t, in)
	providers := got["provider"].(map[string]any)
	aiven := providers["aiven"].(map[string]any)
	if got["$schema"] != "https://example.test/schema.json" || got["model"] != "local/qwen" || got["theme"] != "dark" ||
		!reflect.DeepEqual(providers["local"], map[string]any{"npm": "x"}) || aiven["x"] != nil || len(aiven["models"].(map[string]any)) != 4 {
		t.Errorf("opencode.json = %v", got)
	}
	if data, err := os.ReadFile(configPath(in)); err != nil || !strings.Contains(string(data), "  // the user's own\n") ||
		!strings.Contains(string(data), `"local": {"npm": "x"}`) {
		t.Errorf("opencode.json lost the user's layout: %v\n%s", err, data)
	}
	in.Default = true
	if got := setUp(t, in); got["model"] != "aiven/claude-sonnet" {
		t.Errorf("default not wired: %v", got["model"])
	}
}

func TestSetupRefusesConfigItCannotMerge(t *testing.T) {
	for _, tc := range []struct{ name, data string }{
		{"malformed", `{"provider":`},
		{"provider not an object", `{"provider":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input(t)
			write(t, configPath(in), tc.data)
			if _, err := opencode.Setup(in); err == nil {
				t.Fatal("Setup accepted an unreadable file")
			}
		})
	}
	in := input(t)
	in.UserHome = ""
	if _, err := opencode.Setup(in); err == nil || !errors.Is(err, config.ErrNoHome) {
		t.Errorf("Setup without a home: %v", err)
	}
}

// The preview shows a reference in place and redacts anything else in a credential field.
func TestReferenceAcceptsOnlyASubstitution(t *testing.T) {
	for value, want := range map[string]bool{
		opencode.APIKey(baseURL):     true,
		"{env:AIVEN_AI_GATEWAY_KEY}": true,
		"{file:/etc/key}":            true,
		key:                          false,
		"{env:}":                     false,
		"{file:a b}":                 false,
		"x{env:KEY}":                 false,
		"{env:KEY}x":                 false,
	} {
		if got := opencode.Reference("apikey", value); got != want {
			t.Errorf("Reference(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestLaunchAddsTheDefaultModelUnlessTheArgsChooseOne(t *testing.T) {
	in := input(t)
	if _, err := opencode.Launch(in); err == nil {
		t.Error("Launch added a model setup did not configure")
	}
	for _, args := range [][]string{{"--model=aiven/glm"}, {"auth", "login"}} {
		in.Args = args
		if _, err := opencode.Launch(in); err != nil {
			t.Errorf("Launch(%q) without a config, adding no model: %v", args, err)
		}
	}
	in.Args = nil
	setUp(t, in)
	for _, tc := range []struct{ args, want []string }{
		{nil, []string{"opencode", "-m", "aiven/claude-sonnet"}},
		{[]string{"."}, []string{"opencode", "."}},
		{[]string{"run", "hi"}, []string{"opencode", "run", "hi"}},
		{[]string{"--log-level", "INFO", "models", "aiven"}, []string{"opencode", "--log-level", "INFO", "models", "aiven"}},
		{[]string{"--model=aiven/glm"}, []string{"opencode", "--model=aiven/glm"}},
		{[]string{"auth", "login"}, []string{"opencode", "auth", "login"}},
	} {
		in.Args = tc.args
		plan, err := opencode.Launch(in)
		if err != nil || plan.Files != nil || plan.Env != nil || !slices.Equal(plan.Argv, tc.want) {
			t.Errorf("Launch(%q) = %+v, %v; want argv %q", tc.args, plan, err, tc.want)
		}
	}
	in.Args = nil
	in.Table = []models.Model{{Alias: "claude-sonnet-5-5"}}
	if _, err := opencode.Launch(in); err == nil || !strings.Contains(err.Error(), "refresh with setup") {
		t.Errorf("Launch with a default missing from the catalogue: %v", err)
	}
	write(t, configPath(in), `{`)
	if _, err := opencode.Launch(in); err == nil || strings.Contains(err.Error(), "refresh with setup") {
		t.Errorf("Launch with an unreadable opencode.json: %v", err)
	}
}

func TestReadyChecksTheProvider(t *testing.T) {
	in := input(t)
	if _, err := opencode.Ready(in.UserHome, "", baseURL); err == nil {
		t.Error("Ready before setup")
	}
	setUp(t, in)
	if needsEnv, err := opencode.Ready(in.UserHome, "", baseURL); err != nil || needsEnv {
		t.Errorf("Ready after setup = %v, %v", needsEnv, err)
	}
	if _, err := opencode.Ready(in.UserHome, "", "https://other.example"); err == nil {
		t.Error("Ready for another gateway")
	}
	wired := configPath(in)
	data, err := os.ReadFile(wired)
	if err != nil {
		t.Fatal(err)
	}
	write(t, wired, strings.ReplaceAll(string(data), creds.Rel(baseURL), creds.Rel(config.DefaultBaseURL)))
	if _, err := opencode.Ready(in.UserHome, "", baseURL); err == nil {
		t.Error("Ready with another gateway's key helper")
	}
	write(t, wired, string(data))
	provider := func(npm, apiKey, models string) string {
		return `{"provider":{"aiven":{"npm":"` + npm + `","options":{"baseURL":"` + baseURL + `/v1","apiKey":"` + apiKey +
			`"},"models":` + models + `}}}`
	}
	for name, data := range map[string]string{
		"another package":    provider("@ai-sdk/anthropic", opencode.APIKey(baseURL), `{"glm":{}}`),
		"another key source": provider("@ai-sdk/openai-compatible", "{env:AIVEN_AI_GATEWAY_KEY}", `{"glm":{}}`),
		"no models":          provider("@ai-sdk/openai-compatible", opencode.APIKey(baseURL), `{}`),
		"unreadable":         `{`,
	} {
		write(t, configPath(in), data)
		if _, err := opencode.Ready(in.UserHome, "", baseURL); err == nil {
			t.Errorf("Ready with %s", name)
		}
	}
}

// OpenCode reads opencode.jsonc over opencode.json, so the provider goes where it has the last word.
func TestSetupWritesTheJSONCFileWhenOneExists(t *testing.T) {
	in := input(t)
	jsonc := filepath.Join(in.UserHome, ".config", "opencode", "opencode.jsonc")
	for _, both := range []bool{false, true} {
		if both {
			write(t, configPath(in), `{"model": "openai/gpt"}`)
		}
		write(t, jsonc, "{\n  // mine\n  \"theme\": \"dark\"\n}\n")
		plan, err := opencode.Setup(in)
		if err != nil || len(plan.Files) != 1 || plan.Files[0].Path != jsonc {
			t.Fatalf("both=%v: plan = %+v, %v", both, plan, err)
		}
		write(t, jsonc, string(plan.Files[0].Data))
		if cfg := decode(t, plan.Files[0]); cfg["theme"] != "dark" || cfg["provider"] == nil {
			t.Errorf("both=%v: opencode.jsonc = %v", both, cfg)
		}
		if _, err := opencode.Ready(in.UserHome, "", baseURL); err != nil {
			t.Errorf("both=%v: Ready = %v", both, err)
		}
		if _, err := opencode.Launch(in); err != nil {
			t.Errorf("both=%v: Launch = %v", both, err)
		}
	}
}

// OpenCode reads only a file named opencode.jsonc, so a directory of that name leaves opencode.json in charge.
func TestSetupSkipsAJSONCDirectory(t *testing.T) {
	in := input(t)
	write(t, configPath(in), `{"model": "openai/gpt"}`)
	if err := os.Mkdir(filepath.Join(in.UserHome, ".config", "opencode", "opencode.jsonc"), 0o700); err != nil {
		t.Fatal(err)
	}
	plan, err := opencode.Setup(in)
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Path != configPath(in) {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
}

// OpenCode roots its configuration at XDG_CONFIG_HOME when that is set, so setup, launch and Ready do too.
func TestSetupLaunchAndReadyFollowXDGConfigHome(t *testing.T) {
	in := input(t)
	in.ConfigHome = filepath.Join(in.UserHome, "xdg")
	setUp(t, in)
	if _, err := os.Stat(filepath.Join(in.UserHome, ".config")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("setup wrote under ~/.config: %v", err)
	}
	if _, err := opencode.Ready(in.UserHome, in.ConfigHome, baseURL); err != nil {
		t.Errorf("Ready = %v", err)
	}
	if _, err := opencode.Ready(in.UserHome, "", baseURL); err == nil {
		t.Error("Ready read the XDG root without XDG_CONFIG_HOME")
	}
	if _, err := opencode.Launch(in); err != nil {
		t.Errorf("Launch = %v", err)
	}
	in.ConfigHome = "xdg"
	t.Chdir(in.UserHome)
	if _, err := opencode.Setup(in); err == nil || !strings.Contains(err.Error(), "XDG_CONFIG_HOME is not an absolute path") {
		t.Errorf("Setup with a relative XDG_CONFIG_HOME: %v", err)
	}
	if _, err := opencode.Launch(in); err == nil || !strings.Contains(err.Error(), "XDG_CONFIG_HOME") {
		t.Errorf("Launch with a relative XDG_CONFIG_HOME: %v", err)
	}
	if _, err := opencode.Ready(in.UserHome, in.ConfigHome, baseURL); err == nil || !strings.Contains(err.Error(), "XDG_CONFIG_HOME") {
		t.Errorf("Ready with a relative XDG_CONFIG_HOME: %v", err)
	}
}

// OpenCode reads opencode.json too, and adds models from every file, so a provider setup once wrote there goes.
func TestSetupRemovesTheProviderFromTheFileOpenCodeReadsFirst(t *testing.T) {
	in := input(t)
	jsonc := filepath.Join(in.UserHome, ".config", "opencode", "opencode.jsonc")
	for _, tc := range []struct{ before, after string }{
		{`{"theme":"dark","provider":{"aiven":{"models":{"old":{}}},"other":{"name":"Other"}}}`, `{"provider":{"other":{"name":"Other"}},"theme":"dark"}`},
		{`{"theme":"dark","provider":{"aiven":{"models":{"old":{}}}}}`, `{"theme":"dark"}`},
	} {
		write(t, configPath(in), tc.before)
		write(t, jsonc, "{}\n")
		plan, err := opencode.Setup(in)
		if err != nil || len(plan.Files) != 2 || plan.Files[0].Path != jsonc || plan.Files[1].Path != configPath(in) ||
			!plan.Files[1].Backup || plan.Files[1].Reference == nil {
			t.Fatalf("plan = %+v, %v", plan, err)
		}
		write(t, configPath(in), string(plan.Files[1].Data))
		if got := decode(t, plan.Files[1]); !reflect.DeepEqual(got, decodeString(t, tc.after)) {
			t.Errorf("opencode.json = %v, want %s", got, tc.after)
		}
	}
}

func decodeString(t *testing.T, data string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(data), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// Without a home, the relative path would name a file in the working directory.
func TestReadyAndLaunchRefuseAMissingHome(t *testing.T) {
	in := input(t)
	setUp(t, in)
	t.Chdir(in.UserHome)
	if _, err := opencode.Ready("", "", baseURL); err == nil || !errors.Is(err, config.ErrNoHome) {
		t.Errorf("Ready without a home: %v", err)
	}
	in.UserHome = ""
	if _, err := opencode.Launch(in); err == nil || !errors.Is(err, config.ErrNoHome) {
		t.Errorf("Launch without a home: %v", err)
	}
}
