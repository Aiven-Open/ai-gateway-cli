// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
)

type codexConfig struct {
	Model          string
	ModelProvider  string                       `toml:"model_provider"`
	ModelProviders map[string]codexProviderConf `toml:"model_providers"`
}

type codexProviderConf struct {
	Name               string
	BaseURL            string `toml:"base_url"`
	EnvKey             string `toml:"env_key"`
	Auth               codexAuth
	WireAPI            string `toml:"wire_api"`
	RequiresOpenAIAuth *bool  `toml:"requires_openai_auth"`
}

// codexConfigOf parses the `aiven` profile codex was pointed at.
func codexConfigOf(t *testing.T, s *session) codexConfig {
	t.Helper()
	var c codexConfig
	codexFile(t, s, "aiven.config.toml", &c)
	return c
}

func TestCodexLaunchesWithGatewayProviderConfig(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	r := s.setupAndLaunch("codex")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	c := s.launched()
	if c.name != "codex" || !slices.Equal(c.argv, []string{"codex", "-p", "aiven"}) {
		t.Fatalf("exec %q %q", c.name, c.argv)
	}
	if v, ok := c.envVar("CODEX_HOME"); ok {
		t.Errorf("CODEX_HOME = %q, want unset", v)
	}
	no := false
	command, args := creds.Command(g.URL, runtime.GOOS)
	provider := codexProviderConf{
		Name: "Aiven AI Gateway", BaseURL: g.URL + "/v1", WireAPI: "responses", RequiresOpenAIAuth: &no,
		Auth: codexAuth{Command: command, Args: args},
	}
	want := codexConfig{Model: "gpt-sol", ModelProvider: "aiven", ModelProviders: map[string]codexProviderConf{"aiven": provider}}
	if cfg := codexConfigOf(t, s); !reflect.DeepEqual(cfg, want) {
		t.Errorf("profile = %+v, want %+v", cfg, want)
	}
	var base codexConfig
	codexFile(t, s, "config.toml", &base)
	if want := (codexConfig{ModelProviders: map[string]codexProviderConf{"aiven": provider}}); !reflect.DeepEqual(base, want) {
		t.Errorf("config.toml = %+v, want %+v", base, want)
	}
	if tmp, _ := filepath.Glob(filepath.Join(s.fakeHome, ".codex", "*.tmp")); len(tmp) != 0 {
		t.Errorf("left %v", tmp)
	}
}

func TestCodexPrefersNewestSol(t *testing.T) {
	for _, tc := range []struct{ name, key, want string }{
		{"production", keyProduction, "gpt-6.1-sol"},
		{"without 6.1", keyProductionFallback, "gpt-6-sol"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(tc.key)
			s.run("setup", "codex", "--yes")
			if m := codexConfigOf(t, s).Model; m != tc.want {
				t.Errorf("model = %q, want %q", m, tc.want)
			}
		})
	}
}

func TestCodexDefaultsToSonnetWithoutGpt(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyClaudeOnly)
	s.run("setup", "codex", "--yes")
	if m := codexConfigOf(t, s).Model; m != "claude-sonnet" {
		t.Errorf("model = %q", m)
	}
}

func TestCodexFallsBackToFirstAliasWithoutGptOrSonnet(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyFlash)
	s.run("setup", "codex", "--yes")
	if m := codexConfigOf(t, s).Model; m != "claude-opus" {
		t.Errorf("model = %q", m)
	}
}

func TestCodexConfigRoundTripsAliasWithQuote(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyQuoted)
	writeFile(t, filepath.Join(s.fakeHome, ".codex/aiven.config.toml"), "model = 'bad\"alias'\n")
	if r := s.run("setup", "codex", "--yes"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if m := codexConfigOf(t, s).Model; m != `bad"alias` {
		t.Errorf("model = %q", m)
	}
}

func TestCodexForwardsArgs(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.setupAndLaunch("codex", "exec", "--json")
	if want := []string{"codex", "-p", "aiven", "exec", "--json"}; !slices.Equal(s.launched().argv, want) {
		t.Errorf("argv = %q, want %q", s.launched().argv, want)
	}
}

func TestTrailingSlashBaseURLYieldsSingleSlashEndpoint(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	env, err := config.Load([]string{"AIVEN_AI_GATEWAY_BASE_URL=" + g.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	s.env.BaseURL = env.BaseURL
	if r := s.run("setup", "codex", "--yes"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if got := codexConfigOf(t, s).ModelProviders["aiven"].BaseURL; got != g.URL+"/v1" {
		t.Fatalf("base_url = %q, want %q", got, g.URL+"/v1")
	}
}

// codexFile parses ~/.codex/<name> into v; a read or parse failure fails the test.
func codexFile(t *testing.T, s *session, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.fakeHome, ".codex", name))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), keyValid) {
		t.Errorf("key written to %s", name)
	}
	if err := toml.Unmarshal(data, v); err != nil {
		t.Fatalf("%s does not parse: %v\n%s", name, err, data)
	}
}

type codexAuth struct {
	Command string
	Args    []string
}

func TestCodexMergeKeepsUserConfigAndDropsLegacyProfile(t *testing.T) {
	const before = `model = "o3"
model_provider = "openai"
profile = "aiven"

[mcp_servers.docs]
command = "docs-mcp"

[projects."/src/app"]
trust_level = "trusted"

[model_providers.other]
name = "Other"

[profiles.aiven]
model = "gpt-sol"

[profiles.work]
model = "gpt-5"
`
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	path := filepath.Join(s.fakeHome, ".codex", "config.toml")
	writeFile(t, path, before)
	for range 2 {
		if r := s.run("setup", "codex", "--yes"); r.code != 0 {
			t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
		}
	}
	var got map[string]any
	codexFile(t, s, "config.toml", &got)
	providers, _ := got["model_providers"].(map[string]any)
	if bak := latestBackup(t, path); bak != before {
		t.Errorf("bak = %q", bak)
	}
	if got["model"] != "o3" || got["model_provider"] != "openai" || got["mcp_servers"] == nil || got["projects"] == nil ||
		providers["other"] == nil || providers["aiven"] == nil {
		t.Errorf("merged = %v", got)
	}
	if _, ok := got["profile"]; ok || !reflect.DeepEqual(got["profiles"], map[string]any{"work": map[string]any{"model": "gpt-5"}}) {
		t.Errorf("profile = %v, profiles = %v", got["profile"], got["profiles"])
	}

	s = newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	writeFile(t, filepath.Join(s.fakeHome, ".codex", "config.toml"), "[profiles.aiven]\nmodel = \"gpt-sol\"\n")
	s.run("setup", "codex", "--yes")
	got = nil
	codexFile(t, s, "config.toml", &got)
	if _, ok := got["profiles"]; ok {
		t.Errorf("emptied profiles kept: %v", got)
	}
}

func TestCodexRefusesMalformedConfig(t *testing.T) {
	for bad, problem := range map[string]string{
		`model = `:              " is not valid TOML",
		`model_providers = "x"`: `: "model_providers" is not a TOML table`,
		`model_providers = []`:  `: "model_providers" is not a TOML table`,
	} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
		path := filepath.Join(s.fakeHome, ".codex", "config.toml")
		writeFile(t, path, bad)
		if r := s.run("setup", "codex", "--yes"); r.code != 1 || !strings.Contains(r.stderr, path+problem+"; fix or move it, then retry") || len(s.runner.calls) != 0 {
			t.Fatalf("%s: exit %d, stderr %q", bad, r.code, r.stderr)
		}
		if data, _ := os.ReadFile(path); string(data) != bad {
			t.Errorf("%s rewritten to %q", bad, data)
		}
	}
}
