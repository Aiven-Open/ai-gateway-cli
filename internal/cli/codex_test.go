// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"fmt"
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
	Model              string
	ModelProvider      string                       `toml:"model_provider"`
	ModelProviders     map[string]codexProviderConf `toml:"model_providers"`
	ModelContextWindow int                          `toml:"model_context_window"`
	ReviewModel        string                       `toml:"review_model"`
}

type codexProviderConf struct {
	Name    string
	BaseURL string `toml:"base_url"`
	EnvKey  string `toml:"env_key"`
	WireAPI string `toml:"wire_api"`
}

// codexConfigOf parses the leading `-c key=value` overrides `codex` was launched with as one TOML document; as in Codex, a later key wins.
func codexConfigOf(t *testing.T, s *session) codexConfig {
	t.Helper()
	argv := s.launched().argv
	last := map[string]string{}
	var keys []string
	for i := 1; i < len(argv)-1 && argv[i] == "-c"; i += 2 {
		k, v, _ := strings.Cut(argv[i+1], "=")
		k = strings.TrimSpace(k)
		if _, seen := last[k]; !seen {
			keys = append(keys, k)
		}
		last[k] = v
	}
	var doc strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&doc, "%s = %s\n", k, last[k])
	}
	var c codexConfig
	if err := toml.Unmarshal([]byte(doc.String()), &c); err != nil {
		t.Fatalf("overrides do not parse: %v\n%s", err, doc.String())
	}
	return c
}

// codexFileConfigOf parses what `codex --isolated` wrote; a parse failure fails the test.
func codexFileConfigOf(t *testing.T, s *session) codexConfig {
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

// userArgs returns argv after the launcher's leading `-c` overrides.
func userArgs(argv []string) []string {
	i := 1
	for i < len(argv)-1 && argv[i] == "-c" {
		i += 2
	}
	return argv[i:]
}

func codexWant(g *gatewayDouble, model string) codexConfig {
	return codexConfig{
		Model:         model,
		ReviewModel:   model,
		ModelProvider: "aiven-gateway",
		ModelProviders: map[string]codexProviderConf{"aiven-gateway": {
			Name: "Aiven LLM Gateway", BaseURL: g.URL + "/v1", EnvKey: "AIVEN_GATEWAY_KEY", WireAPI: "responses",
		}},
	}
}

func TestCodexLaunchesInOwnProfileWithGatewayOverrides(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	r := s.run("codex")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	c := s.launched()
	if c.name != "codex" || len(userArgs(c.argv)) != 0 {
		t.Fatalf("exec %q %q", c.name, c.argv)
	}
	if v, ok := c.envVar("CODEX_HOME"); ok {
		t.Errorf("CODEX_HOME = %q, want user's own", v)
	}
	if v, _ := c.envVar("AIVEN_GATEWAY_KEY"); v != keyValid {
		t.Errorf("AIVEN_GATEWAY_KEY = %q", v)
	}
	if got, want := codexConfigOf(t, s), codexWant(g, "gpt-sol"); !reflect.DeepEqual(got, want) {
		t.Errorf("overrides = %+v, want %+v", got, want)
	}
	for _, f := range s.homeFiles() {
		if filepath.Dir(f) != "aig-keys" {
			t.Errorf("wrote %s", f)
		}
	}
}

func TestCodexIsolatedUsesPrivateHome(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	if r := s.run("codex", "--isolated", "exec"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	c := s.launched()
	if !slices.Equal(c.argv, []string{"codex", "exec"}) {
		t.Fatalf("argv %q", c.argv)
	}
	if v, _ := c.envVar("CODEX_HOME"); v != filepath.Join(s.env.Home, "codex") {
		t.Errorf("CODEX_HOME = %q", v)
	}
	if got, want := codexFileConfigOf(t, s), codexWant(g, "gpt-sol"); !reflect.DeepEqual(got, want) {
		t.Errorf("config = %+v, want %+v", got, want)
	}
	if tmp, _ := filepath.Glob(filepath.Join(s.env.Home, "codex", "*.tmp")); len(tmp) != 0 {
		t.Errorf("left %v", tmp)
	}
}

// Codex merges a `-c` table into the user's same-named provider, so its other fields (a catalog URL) would get the key.
func TestCodexRefusesOwnProfileDefiningGatewayProvider(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	cfg := filepath.Join(s.env.CodexHome, "config.toml")
	if err := os.MkdirAll(s.env.CodexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[model_providers.aiven-gateway]\nmodel_catalog_url = \"https://elsewhere.example\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := s.run("codex")
	if want := "aiven-llm: " + cfg + " defines model provider aiven-gateway, which Codex would merge into the gateway's. Remove it, or run: aiven-llm codex --isolated\n"; r.code != 1 || r.stderr != want {
		t.Fatalf("exit %d, stderr %q, want %q", r.code, r.stderr, want)
	}
	if len(s.runner.calls) != 0 {
		t.Errorf("exec'd %v", s.runner.calls)
	}
	if r := s.run("codex", "--isolated"); r.code != 0 {
		t.Errorf("--isolated: exit %d, stderr %q", r.code, r.stderr)
	}
}

// `-p x` (in any spelling) makes Codex merge `x.config.toml` from CODEX_HOME as well, so every profile file is checked.
func TestCodexRefusesProfileDefiningGatewayProvider(t *testing.T) {
	for _, args := range [][]string{{"-p", "x"}, {"--profile", "x"}, {"--profile=x"}, {"-px"}, {"-p=x"}, {}} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
		s.env.CodexHome += "[work]*" // glob metacharacters in the path are literal
		cfg := filepath.Join(s.env.CodexHome, "x.config.toml")
		if err := os.MkdirAll(s.env.CodexHome, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfg, []byte("[model_providers.aiven-gateway]\nmodel_catalog_url = \"https://elsewhere.example\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		r := s.run(append([]string{"codex"}, args...)...)
		if want := "aiven-llm: " + cfg + " defines model provider aiven-gateway, which Codex would merge into the gateway's. Remove it, or run: aiven-llm codex --isolated\n"; r.code != 1 || r.stderr != want {
			t.Errorf("%q: exit %d, stderr %q", args, r.code, r.stderr)
		}
	}
}

// An execute-only CODEX_HOME hides its profiles from listing, but Codex can still open one by name.
func TestCodexRefusesUnlistableProfileDir(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	if err := os.MkdirAll(s.env.CodexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.env.CodexHome, 0o100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(s.env.CodexHome, 0o700) })
	r := s.run("codex")
	if r.code != 1 || !strings.Contains(r.stderr, "cannot list your Codex profile "+s.env.CodexHome) {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if len(s.runner.calls) != 0 {
		t.Errorf("exec'd %v", s.runner.calls)
	}
}

func TestCodexRefusesOwnProfileWithoutHome(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.env.CodexHome = ""
	r := s.run("codex")
	if want := "aiven-llm: cannot locate your Codex profile (HOME unset); run: aiven-llm codex --isolated\n"; r.code != 1 || r.stderr != want {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if r := s.run("codex", "--isolated"); r.code != 0 {
		t.Errorf("--isolated: exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestCodexPassesContextWindowOfDefaultModel(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyClaudeOnly)
	s.run("codex")
	if got := codexConfigOf(t, s).ModelContextWindow; got != 1000000 {
		t.Errorf("model_context_window = %d", got)
	}
}

// Codex applies model_context_window regardless of the model, so it must match the one the user's args select.
func TestCodexContextWindowFollowsUserModel(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"-m", "claude-haiku"}, 200000},
		{[]string{"--model", "claude-haiku"}, 200000},
		{[]string{"--model=claude-haiku"}, 200000},
		{[]string{"-m", "not-granted"}, 0},
		{[]string{"-c", `model="claude-haiku"`}, 0},
		{[]string{"-c", `model = "claude-haiku"`}, 0},
		{[]string{"--config", `model="claude-haiku"`}, 0},
		{[]string{`--config=model="claude-haiku"`}, 0},
	} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyClaudeOnly)
		s.run(append([]string{"codex"}, tc.args...)...)
		if got := codexConfigOf(t, s).ModelContextWindow; got != tc.want {
			t.Errorf("%q: model_context_window = %d, want %d", tc.args, got, tc.want)
		}
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
	for _, mode := range [][]string{{"codex"}, {"codex", "--isolated"}} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyQuote)
		if r := s.run(mode...); r.code != 0 {
			t.Fatalf("%q: exit %d, stderr %q", mode, r.code, r.stderr)
		}
		get := codexConfigOf
		if len(mode) > 1 {
			get = codexFileConfigOf
		}
		if m := get(t, s).Model; m != `bad"alias` {
			t.Errorf("%q: model = %q", mode, m)
		}
	}
}

func TestCodexForwardsArgsAfterOverrides(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.run("codex", "exec", "--json")
	if got, want := userArgs(s.launched().argv), []string{"exec", "--json"}; !slices.Equal(got, want) {
		t.Errorf("user args = %q, want %q", got, want)
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
