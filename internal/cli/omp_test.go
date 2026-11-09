// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// ompPlugin marks the router plugin installed in the `aiven-llm` OMP profile.
func (s *session) ompPlugin() *session {
	s.t.Helper()
	if err := os.MkdirAll(filepath.Join(s.env.OMPHome, "profiles", "aiven-llm", "plugins", "node_modules", "aiven-gateway-router"), 0o700); err != nil {
		s.t.Fatal(err)
	}
	return s
}

func (s *session) ompOverlay() string { return filepath.Join(s.env.Home, "omp", "config.yml") }
func (s *session) ompModelsYML() string {
	return filepath.Join(s.env.OMPHome, "profiles", "aiven-llm", "agent", "models.yml")
}

func TestOmpLaunchesRouterProfileWithReadOnlyTools(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).ompPlugin()
	if r := s.run("omp"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	c := s.launched()
	want := []string{"omp", "--profile", "aiven-llm", "--config", s.ompOverlay(), "--tools", "read,grep,glob,task,todo"}
	if c.name != "omp" || !slices.Equal(c.argv, want) {
		t.Fatalf("exec %q %q, want argv %q", c.name, c.argv, want)
	}
	if v, _ := c.envVar("AIVEN_GATEWAY_KEY"); v != keyValid {
		t.Errorf("AIVEN_GATEWAY_KEY = %q", v)
	}
}

func TestOmpWritesOverlayAndProviderModels(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid).ompPlugin()
	s.run("omp")
	overlay := readJSON(t, s.ompOverlay())
	roles, _ := overlay["modelRoles"].(map[string]any)
	if roles["default"] != "aiven-gateway/glm" {
		t.Errorf("modelRoles.default = %v, want aiven-gateway/glm", roles["default"])
	}
	if enabled, _ := overlay["enabledModels"].([]any); !slices.Equal(enabled, []any{"aiven-gateway/*"}) {
		t.Errorf("enabledModels = %v", overlay["enabledModels"])
	}
	provider := readJSON(t, s.ompModelsYML())["providers"].(map[string]any)["aiven-gateway"].(map[string]any)
	want := map[string]any{
		"baseUrl": g.URL + "/v1", "api": "openai-completions", "apiKey": "AIVEN_GATEWAY_KEY",
		"discovery": map[string]any{"type": "openai-models-list"},
	}
	if !reflect.DeepEqual(provider, want) {
		t.Errorf("provider = %v, want %v", provider, want)
	}
}

func TestOmpRouterSessionDefaultFollowsTierLadder(t *testing.T) {
	cases := []struct{ name, key, want string }{
		{"flash tier first", keyAllTiers, "deepseek-flash"},
		{"fast tier over glm", keyFastAndGLM, "glm-fast"},
		{"flash tier alone", keyFlash, "deepseek-flash"},
		{"fast tier next", keyMidContext, "glm-fast"},
		{"glm over other aliases", keyValid, "glm"},
		{"first alias when no tier is granted", keyClaudeOnly, "claude-haiku"},
		{"router never beats another alias", keyRouterSol, "gpt-sol"},
		{"production cheap tier", keyProduction, "claude-haiku-5.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(tc.key).ompPlugin()
			s.run("omp")
			roles := readJSON(t, s.ompOverlay())["modelRoles"].(map[string]any)
			if want := "aiven-gateway/" + tc.want; roles["default"] != want {
				t.Errorf("modelRoles.default = %v, want %v", roles["default"], want)
			}
		})
	}
}

func TestOmpRouterOnlyKeyFallsBackToRouter(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyRouterOnly).ompPlugin()
	if r := s.run("omp"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	roles := readJSON(t, s.ompOverlay())["modelRoles"].(map[string]any)
	if roles["default"] != "aiven-gateway/aiven-model" {
		t.Errorf("modelRoles.default = %v", roles["default"])
	}
}

const ompPluginHint = `aiven-gateway-router is not installed in the aiven-llm profile, so there are no role agents
yet. The plugin is not published yet; OMP launches need it installed in that profile.
`

func TestOmpRequiresPluginAndWritesNothing(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	r := s.run("omp")
	if want := "aiven-llm: " + ompPluginHint; r.code != 1 || r.stdout != "" || r.stderr != want {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if n := len(g.journal()); n != 0 {
		t.Errorf("gateway got %d requests", n)
	}
	if len(s.runner.calls) != 0 {
		t.Errorf("exec'd %v", s.runner.calls)
	}
	for _, f := range s.homeFiles() {
		if filepath.Dir(f) != "aig-keys" {
			t.Errorf("wrote %s", f)
		}
	}
	if _, err := os.Stat(s.ompModelsYML()); err == nil {
		t.Error("wrote models.yml")
	}
}

func TestOmpPrintsPriceTableOnStderr(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).ompPlugin()
	r := s.run("omp")
	if want := "claude-sonnet ($3.00 in / $15.00 out per 1M tok)\n"; !strings.Contains(r.stderr, want) {
		t.Errorf("stderr %q lacks %q", r.stderr, want)
	}
}

func TestOmpRouterToolsExcludeEveryWriteTool(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).ompPlugin()
	s.run("omp")
	argv := s.launched().argv
	i := slices.Index(argv, "--tools")
	if i < 0 || i+1 >= len(argv) {
		t.Fatalf("no --tools in %q", argv)
	}
	tools := strings.Split(argv[i+1], ",")
	for _, w := range []string{"edit", "write", "bash"} {
		if slices.Contains(tools, w) {
			t.Errorf("router gets %s tool: %q", w, tools)
		}
	}
}

func TestOmpLeavesProfileConfigAlone(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).ompPlugin()
	cfg := filepath.Join(s.env.OMPHome, "profiles", "aiven-llm", "agent", "config.yml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("theme:\n  dark: titanium\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.run("omp")
	if got, _ := os.ReadFile(cfg); string(got) != "theme:\n  dark: titanium\n" {
		t.Errorf("profile config = %q", got)
	}
}

func TestOmpFailsBeforeGatewayOrWritesWhenBinaryMissing(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid).ompPlugin()
	s.runner.missing = []string{"omp"}
	r := s.run("omp")
	if want := "aiven-llm: omp is required but not installed\n"; r.code != 1 || r.stderr != want {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if n := len(g.journal()); n != 0 {
		t.Errorf("gateway got %d requests", n)
	}
	if _, err := os.Stat(s.ompModelsYML()); err == nil {
		t.Error("wrote models.yml")
	}
	if len(s.runner.calls) != 0 {
		t.Errorf("exec'd %v", s.runner.calls)
	}
}

func TestOmpForwardsArgsAfterLauncherFlags(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).ompPlugin()
	s.run("omp", "-p", "hi")
	argv := s.launched().argv
	if want := []string{"read,grep,glob,task,todo", "-p", "hi"}; !slices.Equal(argv[len(argv)-3:], want) {
		t.Errorf("argv = %q", argv)
	}
}

func TestOmpKeepsKeyInEnvOnly(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).ompPlugin()
	s.run("omp")
	c := s.launched()
	for _, a := range c.argv {
		if strings.Contains(a, keyValid) {
			t.Errorf("key in argv %q", c.argv)
		}
	}
	for _, p := range []string{s.ompOverlay(), s.ompModelsYML()} {
		if data, _ := os.ReadFile(p); strings.Contains(string(data), keyValid) {
			t.Errorf("key written to %s", p)
		}
	}
}

func TestOmpGeneratedFilesParseAndNameGatewayProvider(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).ompPlugin()
	s.run("omp")
	providers := readJSON(t, s.ompModelsYML())["providers"].(map[string]any)
	if _, ok := providers["aiven-gateway"]; !ok || len(providers) != 1 {
		t.Errorf("providers = %v", providers)
	}
	readJSON(t, s.ompOverlay())
}
