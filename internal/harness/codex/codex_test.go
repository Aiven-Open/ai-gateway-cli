// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package codex_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/codex"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

const (
	baseURL = "https://gateway.example"
	key     = "sk-test-valid"
)

func input(t *testing.T, aliases ...string) harness.Input {
	t.Helper()
	var table []models.Model
	for _, a := range aliases {
		table = append(table, models.Model{Alias: a})
	}
	return harness.Input{Table: table, BaseURL: baseURL, UserHome: t.TempDir()}
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

func decode(t *testing.T, f harness.File) map[string]any {
	t.Helper()
	cfg := map[string]any{}
	if err := toml.Unmarshal(f.Data, &cfg); err != nil {
		t.Fatalf("%s: %v", f.Path, err)
	}
	if strings.Contains(string(f.Data), key) {
		t.Errorf("%s holds the key:\n%s", f.Path, f.Data)
	}
	return cfg
}

// setUp runs Setup and writes its files, as setup does after its preview.
func setUp(t *testing.T, in harness.Input) (config, profile map[string]any) {
	t.Helper()
	plan, err := codex.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(in.UserHome, ".codex/config.toml"), filepath.Join(in.UserHome, ".codex/aiven.config.toml")}
	if len(plan.Files) != 2 || plan.Files[0].Path != want[0] || plan.Files[1].Path != want[1] ||
		!plan.Files[0].Backup || !plan.Files[1].Backup || plan.Argv != nil || plan.Env != nil {
		t.Fatalf("plan = %+v", plan)
	}
	for _, f := range plan.Files {
		write(t, f.Path, string(f.Data))
	}
	return decode(t, plan.Files[0]), decode(t, plan.Files[1])
}

var aivenProvider = func() map[string]any {
	command, args := creds.Command(baseURL, runtime.GOOS)
	var list []any
	for _, a := range args {
		list = append(list, a)
	}
	return map[string]any{
		"name": "Aiven AI Gateway", "base_url": baseURL + "/v1", "wire_api": "responses", "requires_openai_auth": false,
		"auth": map[string]any{"command": command, "args": list},
	}
}()

func TestSetupAddsTheProviderAndProfileAndLeavesTheDefaultAlone(t *testing.T) {
	in := input(t, "gpt-6.1-sol", "claude-sonnet")
	cfg, profile := setUp(t, in)
	want := map[string]any{"model_providers": map[string]any{"aiven": aivenProvider}}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("config.toml = %v, want %v", cfg, want)
	}
	want["model"], want["model_provider"], want["web_search"] = "gpt-6.1-sol", "aiven", "disabled"
	if !reflect.DeepEqual(profile, want) {
		t.Errorf("aiven.config.toml = %v, want %v", profile, want)
	}
}

func TestSetupAsTheDefaultLeavesTheCanonicalWebSearchUnset(t *testing.T) {
	in := input(t, "gemini-flash")
	in.Default = true
	if cfg, _ := setUp(t, in); cfg["model_provider"] != "aiven" || cfg["web_search"] != nil {
		t.Errorf("config.toml = %v", cfg)
	}
}

func TestSetupKeepsTheUsersConfigAndDropsTheConflictingForms(t *testing.T) {
	in := input(t, "gpt-6.1-sol")
	write(t, filepath.Join(in.UserHome, ".codex/config.toml"), `# my settings
model = "o3"
model_provider = "openai"
profile = "aiven"
web_search = "live"

[profiles.aiven]
model = "x"

[profiles.work]
model = "o3"

[model_providers.aiven]
env_key = "AIVEN_GATEWAY_KEY"
experimental_bearer_token = "token"
requires_openai_auth = true
query_params = { a = "1" }

[mcp_servers.docs]
command = "docs-mcp"
`)
	write(t, filepath.Join(in.UserHome, ".codex/aiven.config.toml"), `model = "gpt-terra"`+"\n"+`web_search = "cached"`+"\n")
	cfg, profile := setUp(t, in)
	if data, _ := os.ReadFile(filepath.Join(in.UserHome, ".codex/config.toml")); !strings.HasPrefix(string(data), "# my settings\nmodel = \"o3\"\n") {
		t.Errorf("config.toml lost its comment or layout:\n%s", data)
	}
	if cfg["model"] != "o3" || cfg["model_provider"] != "openai" || cfg["profile"] != nil || cfg["web_search"] != "live" ||
		!reflect.DeepEqual(cfg["profiles"], map[string]any{"work": map[string]any{"model": "o3"}}) ||
		!reflect.DeepEqual(cfg["mcp_servers"], map[string]any{"docs": map[string]any{"command": "docs-mcp"}}) {
		t.Errorf("config.toml = %v", cfg)
	}
	provider := cfg["model_providers"].(map[string]any)["aiven"].(map[string]any)
	if provider["env_key"] != nil || provider["experimental_bearer_token"] != nil || provider["requires_openai_auth"] != false || !reflect.DeepEqual(provider["query_params"], map[string]any{"a": "1"}) {
		t.Errorf("provider = %v", provider)
	}
	if profile["model"] != "gpt-terra" || profile["model_provider"] != "aiven" || profile["web_search"] != "disabled" {
		t.Errorf("chosen model replaced or web search kept: %v", profile)
	}
	in.Default = true
	if cfg, _ = setUp(t, in); cfg["model_provider"] != "aiven" || cfg["model"] != "gpt-terra" || cfg["web_search"] != "live" {
		t.Errorf("default not wired: %v", cfg)
	}
}

func TestSetupPrefersProductionCodexModelsThenAnyGPTThenSonnet(t *testing.T) {
	for _, tc := range []struct {
		aliases []string
		want    string
	}{
		{[]string{"gpt-6-sol", "gpt-6.1-sol", "gpt-astra"}, "gpt-6.1-sol"},
		{[]string{"gpt-6-sol", "gpt-astra"}, "gpt-6-sol"},
		{[]string{"claude-sonnet", "gpt-terra", "gpt-luna"}, "gpt-luna"},
		{[]string{"glm", "claude-sonnet"}, "claude-sonnet"},
	} {
		if _, profile := setUp(t, input(t, tc.aliases...)); profile["model"] != tc.want {
			t.Errorf("%q: model %v, want %s", tc.aliases, profile["model"], tc.want)
		}
	}
}

func TestSetupRefusesConfigItCannotMerge(t *testing.T) {
	for _, tc := range []struct{ name, file, data, want string }{
		{"invalid TOML", "config.toml", "model = ", "not valid TOML"},
		{"invalid profile", "aiven.config.toml", "[", "not valid TOML"},
		{"providers not a table", "config.toml", `model_providers = "x"`, `"model_providers" is not a TOML table`},
		{"provider not a table", "aiven.config.toml", "model_providers = { aiven = 1 }", `"model_providers.aiven" is not a TOML table`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input(t, "gpt-6.1-sol")
			write(t, filepath.Join(in.UserHome, ".codex", tc.file), tc.data)
			if _, err := codex.Setup(in); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	in := input(t, "gpt-6.1-sol")
	in.UserHome = ""
	if _, err := codex.Setup(in); err == nil || !errors.Is(err, config.ErrNoHome) {
		t.Errorf("Setup without a home: %v", err)
	}
}

func TestLaunchSelectsTheAivenProfileAndForwardsArgs(t *testing.T) {
	in := input(t, "gpt-6.1-sol")
	in.Args = []string{"exec", "hi"}
	plan, err := codex.Launch(in)
	if err != nil || plan.Files != nil || plan.Env != nil || !slices.Equal(plan.Argv, []string{"codex", "-p", "aiven", "exec", "hi"}) {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
}

func TestReadyAcceptsSetupAndCompatibleManualWiring(t *testing.T) {
	in := input(t, "gpt-6.1-sol")
	if _, err := codex.Ready(in.UserHome, baseURL); err == nil {
		t.Error("Ready before setup")
	}
	setUp(t, in)
	if needsEnv, err := codex.Ready(in.UserHome, baseURL); err != nil || needsEnv {
		t.Errorf("Ready after setup = %v, %v", needsEnv, err)
	}
	if _, err := codex.Ready(in.UserHome, "https://other.example"); err == nil {
		t.Error("Ready for another gateway")
	}
	config := filepath.Join(in.UserHome, ".codex/config.toml")
	profile := filepath.Join(in.UserHome, ".codex/aiven.config.toml")
	manual := "[model_providers.aiven]\nbase_url = \"" + baseURL + "/v1\"\nwire_api = \"responses\"\nenv_key = \"" + creds.EnvVar + "\"\n"
	write(t, config, manual)
	write(t, profile, "model = \"gpt-6.1-sol\"\nmodel_provider = \"aiven\"\n")
	if needsEnv, err := codex.Ready(in.UserHome, baseURL); err != nil || !needsEnv {
		t.Errorf("Ready with an env_key provider = %v, %v", needsEnv, err)
	}
	write(t, config, strings.Replace(manual, creds.EnvVar, "OTHER_KEY", 1))
	if _, err := codex.Ready(in.UserHome, baseURL); err == nil {
		t.Error("Ready with another env_key")
	}
	write(t, config, manual)
	write(t, profile, "model = \"gpt-6.1-sol\"\n")
	if _, err := codex.Ready(in.UserHome, baseURL); err == nil || !strings.Contains(err.Error(), "no Aiven model selection") {
		t.Errorf("Ready without the profile's provider: %v", err)
	}
}

func TestSetupMovesTheLegacyProfileAndDropsTheTableLeftEmpty(t *testing.T) {
	in := input(t, "gpt-6.1-sol")
	write(t, filepath.Join(in.UserHome, ".codex/config.toml"), "[profiles.aiven]\nmodel = \"x\"\n")
	cfg, profile := setUp(t, in)
	if cfg["profiles"] != nil {
		t.Errorf("config.toml = %v", cfg)
	}
	if profile["model"] != "x" {
		t.Errorf("legacy model not moved: %v", profile)
	}
	write(t, filepath.Join(in.UserHome, ".codex/config.toml"),
		"[profiles.aiven]\nmodel = \"x\"\nmodel_reasoning_effort = \"high\"\nmodel_provider = \"openai\"\n")
	write(t, filepath.Join(in.UserHome, ".codex/aiven.config.toml"), "model = \"gpt-terra\"\n")
	if _, profile = setUp(t, in); profile["model"] != "gpt-terra" || profile["model_reasoning_effort"] != "high" ||
		profile["model_provider"] != "aiven" {
		t.Errorf("profile file's own settings replaced or legacy setting lost: %v", profile)
	}
}

func TestSetupRefusesAnotherTopLevelProfile(t *testing.T) {
	in := input(t, "gpt-6.1-sol")
	path := filepath.Join(in.UserHome, ".codex/config.toml")
	write(t, path, "profile = \"work\"\n\n[profiles.aiven]\nmodel = \"x\"\n")
	if _, err := codex.Setup(in); err == nil || !strings.Contains(err.Error(), path+" sets profile") ||
		!strings.Contains(err.Error(), "codex --profile <name>") {
		t.Errorf("err = %v", err)
	}
}

func TestReadyRefusesIncompatibleWiring(t *testing.T) {
	head := "[model_providers.aiven]\nbase_url = \"" + baseURL + "/v1\"\n"
	auth := func(origin string) (string, string) {
		command, args := creds.Command(origin, runtime.GOOS)
		quoted := make([]string, len(args))
		for i, a := range args {
			quoted[i] = strconv.Quote(a)
		}
		return "[model_providers.aiven.auth]\ncommand = " + strconv.Quote(command) + "\nargs = [" + strings.Join(quoted, ", ") + "]\n", quoted[len(quoted)-1]
	}
	helper, script := auth(baseURL)
	otherGateway, _ := auth(config.DefaultBaseURL)
	for _, tc := range []struct{ name, config, want string }{
		{"no key source", head + "wire_api = \"responses\"\n", "incompatible"},
		{"another helper", head + "wire_api = \"responses\"\n" + strings.Replace(helper, script, `"cat /tmp/key"`, 1), "incompatible"},
		{"another gateway's helper", head + "wire_api = \"responses\"\n" + otherGateway, "incompatible"},
		{"another command", head + "wire_api = \"responses\"\n" + strings.Replace(helper, "command = ", "command = \"bash\" #", 1), "incompatible"},
		{"chat wire API", head + "wire_api = \"chat\"\n" + helper, "incompatible"},
		// Codex refuses these combinations, so the tool would not start.
		{"helper and env_key", head + "wire_api = \"responses\"\nenv_key = \"" + creds.EnvVar + "\"\n" + helper, "incompatible"},
		{"another command and env_key", head + "wire_api = \"responses\"\nenv_key = \"" + creds.EnvVar + "\"\n" + strings.Replace(helper, script, `"cat /tmp/key"`, 1), "incompatible"},
		{"helper and OpenAI auth", head + "wire_api = \"responses\"\nrequires_openai_auth = true\n" + helper, "incompatible"},
		{"helper and bearer token", head + "wire_api = \"responses\"\nexperimental_bearer_token = \"t\"\n" + helper, "incompatible"},
		{"provider not a table", "model_providers = { aiven = 1 }\n", "config.toml has incompatible"},
		{"invalid TOML", "[", "not valid TOML"},
		{"legacy profile selected", "profile = \"aiven\"\n" + head + "wire_api = \"responses\"\n" + helper, "declares the aiven profile"},
		{"legacy profile table", head + "wire_api = \"responses\"\n" + helper + "[profiles.aiven]\nmodel = \"x\"\n", "declares the aiven profile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			write(t, filepath.Join(home, ".codex/config.toml"), tc.config)
			write(t, filepath.Join(home, ".codex/aiven.config.toml"), "model = \"gpt-6.1-sol\"\nmodel_provider = \"aiven\"\n")
			if _, err := codex.Ready(home, baseURL); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// Without a home, the relative paths would name files in the working directory.
func TestReadyRefusesAMissingHome(t *testing.T) {
	in := input(t, "gpt-6.1-sol")
	setUp(t, in)
	t.Chdir(in.UserHome)
	if _, err := codex.Ready("", baseURL); err == nil || !errors.Is(err, config.ErrNoHome) {
		t.Errorf("Ready without a home: %v", err)
	}
}
