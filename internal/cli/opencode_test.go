// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/creds"
)

func opencodeConfig(t *testing.T, s *session) map[string]any {
	t.Helper()
	return readJSON(t, opencodePath(s))
}

func TestOpencodeLaunchesWithGatewayProviderConfig(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	r := s.setupAndLaunch("opencode")
	if r.code != 0 || r.stderr != launched(s.env.BaseURL, 5, "OpenCode") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	c := s.launched()
	if c.name != "opencode" || !slices.Equal(c.argv, []string{"opencode", "-m", "aiven/claude-sonnet"}) {
		t.Fatalf("exec %q %q", c.name, c.argv)
	}
	assertUnset(t, c, "OPENCODE_CONFIG", "AIVEN_AI_GATEWAY_KEY")
	cfg := opencodeConfig(t, s)
	provider := cfg["provider"].(map[string]any)["aiven"].(map[string]any)
	options, got := provider["options"].(map[string]any), provider["models"].(map[string]any)
	want := map[string]any{
		"aiven-model":   map[string]any{"name": "aiven-model"},
		"claude-opus":   map[string]any{"name": "Claude Opus"},
		"claude-sonnet": map[string]any{"name": "claude-sonnet", "limit": map[string]any{"context": 1e6, "output": 64000.0}},
		"glm":           map[string]any{"name": "glm"},
		"gpt-sol":       map[string]any{"name": "gpt-sol"},
	}
	if cfg["$schema"] != "https://opencode.ai/config.json" || cfg["model"] != nil || provider["npm"] != "@ai-sdk/openai-compatible" || provider["name"] != "Aiven AI Gateway" || options["baseURL"] != g.URL+"/v1" || options["apiKey"] != "{file:~/.config/aiven/ai-gateway-keys/"+creds.Name(g.URL)+"}" || !reflect.DeepEqual(got, want) {
		t.Errorf("cfg = %v, provider = %v, models = %v", cfg, provider, got)
	}
}

func TestOpencodeDefaultsToFirstAliasWithoutSonnet(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyFlash)
	s.setupAndLaunch("opencode")
	if got := s.launched().argv; !slices.Equal(got, []string{"opencode", "-m", "aiven/claude-opus"}) {
		t.Errorf("argv = %q", got)
	}
}

func TestOpencodeDefaultsToProductionSonnet(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyProduction)
	s.setupAndLaunch("opencode")
	if got := s.launched().argv; !slices.Equal(got, []string{"opencode", "-m", "aiven/claude-sonnet-5-5"}) {
		t.Errorf("argv = %q", got)
	}
}

func TestOpencodeForwardsArgs(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.setupAndLaunch("opencode", "run", "hi")
	if want := []string{"opencode", "run", "hi"}; !slices.Equal(s.launched().argv, want) {
		t.Errorf("argv = %q, want %q", s.launched().argv, want)
	}
}

func opencodePath(s *session) string {
	return filepath.Join(s.fakeHome, ".config/opencode/opencode.json")
}

func TestOpencodeLeavesTheUsersDefaultAndModelChoiceAlone(t *testing.T) {
	for _, tc := range []struct{ args, want []string }{
		{nil, []string{"opencode", "-m", "aiven/claude-sonnet"}},
		{[]string{"-m", "anthropic/claude"}, []string{"opencode", "-m", "anthropic/claude"}},
		{[]string{"--model=aiven/glm", "."}, []string{"opencode", "--model=aiven/glm", "."}},
		{[]string{"run", "hi"}, []string{"opencode", "run", "hi"}},
		{[]string{"--log-level", "INFO", "models"}, []string{"opencode", "--log-level", "INFO", "models"}},
	} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
		writeFile(t, opencodePath(s), `{"$schema":"./local.json","model":"anthropic/claude"}`)
		if r := s.setupAndLaunch("opencode", tc.args...); r.code != 0 {
			t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
		}
		if got := s.launched().argv; !slices.Equal(got, tc.want) {
			t.Errorf("argv = %q, want %q", got, tc.want)
		}
		if cfg := opencodeConfig(t, s); cfg["model"] != "anthropic/claude" || cfg["$schema"] != "./local.json" {
			t.Errorf("model %v, $schema %v", cfg["model"], cfg["$schema"])
		}
	}
}
