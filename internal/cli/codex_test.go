// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/Aiven-Open/ai-gateway-cli/internal/config"
)

type codexConfig struct {
	Model          string
	ModelProvider  string                       `toml:"model_provider"`
	ModelProviders map[string]codexProviderConf `toml:"model_providers"`
}

type codexProviderConf struct {
	Name    string
	BaseURL string `toml:"base_url"`
	EnvKey  string `toml:"env_key"`
	WireAPI string `toml:"wire_api"`
}

// codexConfigOf parses what `codex` wrote; a parse failure fails the test.
func codexConfigOf(t *testing.T, s *session) codexConfig {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.env.Home, "codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var c codexConfig
	if err := toml.Unmarshal(data, &c); err != nil {
		t.Fatalf("config.toml does not parse: %v\n%s", err, data)
	}
	return c
}

func TestCodexLaunchesWithGatewayProviderConfig(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	r := s.run("codex")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	c := s.launched()
	if c.name != "codex" || !slices.Equal(c.argv, []string{"codex"}) {
		t.Fatalf("exec %q %q", c.name, c.argv)
	}
	if v, _ := c.envVar("CODEX_HOME"); v != filepath.Join(s.env.Home, "codex") {
		t.Errorf("CODEX_HOME = %q", v)
	}
	if v, _ := c.envVar("AIVEN_GATEWAY_KEY"); v != keyValid {
		t.Errorf("AIVEN_GATEWAY_KEY = %q", v)
	}
	cfg := codexConfigOf(t, s)
	want := codexConfig{
		Model:         "gpt-sol",
		ModelProvider: "aiven-gateway",
		ModelProviders: map[string]codexProviderConf{"aiven-gateway": {
			Name: "Aiven LLM Gateway", BaseURL: g.URL + "/v1", EnvKey: "AIVEN_GATEWAY_KEY", WireAPI: "responses",
		}},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("config = %+v, want %+v", cfg, want)
	}
	if tmp, _ := filepath.Glob(filepath.Join(s.env.Home, "codex", "*.tmp")); len(tmp) != 0 {
		t.Errorf("left %v", tmp)
	}
}

func TestCodexPrintsPriceTableOnStderr(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	r := s.run("codex")
	for _, line := range []string{"gpt-sol ($1.25 in / $10.00 out per 1M tok)\n", "glm (price n/a)\n"} {
		if !strings.Contains(r.stderr, line) {
			t.Errorf("stderr %q lacks %q", r.stderr, line)
		}
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q", r.stdout)
	}
}

func TestCodexPrefersNewestSol(t *testing.T) {
	for _, tc := range []struct{ name, key, want string }{
		{"production", keyProduction, "gpt-6.1-sol"},
		{"without 6.1", keyProductionFallback, "gpt-6-sol"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(tc.key)
			s.run("codex")
			if m := codexConfigOf(t, s).Model; m != tc.want {
				t.Errorf("model = %q, want %q", m, tc.want)
			}
		})
	}
}

func TestCodexDefaultsToSonnetWithoutGpt(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyClaudeOnly)
	s.run("codex")
	if m := codexConfigOf(t, s).Model; m != "claude-sonnet" {
		t.Errorf("model = %q", m)
	}
}

func TestCodexFallsBackToFirstAliasWithoutGptOrSonnet(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyFlash)
	s.run("codex")
	if m := codexConfigOf(t, s).Model; m != "claude-opus" {
		t.Errorf("model = %q", m)
	}
}

func TestCodexConfigRoundTripsAliasWithQuote(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyQuote)
	if r := s.run("codex"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if m := codexConfigOf(t, s).Model; m != `bad"alias` {
		t.Errorf("model = %q", m)
	}
}

func TestCodexForwardsArgs(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.run("codex", "exec", "--json")
	if want := []string{"codex", "exec", "--json"}; !slices.Equal(s.launched().argv, want) {
		t.Errorf("argv = %q, want %q", s.launched().argv, want)
	}
}

func TestTrailingSlashBaseURLYieldsSingleSlashEndpoint(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	env, err := config.Load([]string{"AIVEN_LLM_BASE_URL=" + g.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	s.env.BaseURL = env.BaseURL
	if r := s.run("codex"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if got := codexConfigOf(t, s).ModelProviders["aiven-gateway"].BaseURL; got != g.URL+"/v1" {
		t.Fatalf("base_url = %q, want %q", got, g.URL+"/v1")
	}
}
