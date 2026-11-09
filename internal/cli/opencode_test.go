// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func opencodeConfig(t *testing.T, s *session) map[string]any {
	t.Helper()
	return readJSON(t, filepath.Join(s.env.Home, "opencode", "opencode.json"))
}

func TestOpencodeLaunchesWithGatewayProviderConfig(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	r := s.run("opencode")
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	c := s.launched()
	cfgPath := filepath.Join(s.env.Home, "opencode", "opencode.json")
	if c.name != "opencode" || !slices.Equal(c.argv, []string{"opencode"}) {
		t.Fatalf("exec %q %q", c.name, c.argv)
	}
	if v, _ := c.envVar("OPENCODE_CONFIG"); v != cfgPath {
		t.Errorf("OPENCODE_CONFIG = %q", v)
	}
	if v, _ := c.envVar("AIVEN_GATEWAY_KEY"); v != keyValid {
		t.Errorf("AIVEN_GATEWAY_KEY = %q", v)
	}
	cfg := opencodeConfig(t, s)
	provider := cfg["provider"].(map[string]any)["aiven-gateway"].(map[string]any)
	options := provider["options"].(map[string]any)
	names := map[string]any{}
	for id, m := range provider["models"].(map[string]any) {
		names[id] = m.(map[string]any)["name"]
	}
	wantNames := map[string]any{
		"aiven-model":   "aiven-model (price n/a)",
		"claude-opus":   "claude-opus ($15.00 in / $75.00 out per 1M tok)",
		"claude-sonnet": "claude-sonnet ($3.00 in / $15.00 out per 1M tok)",
		"glm":           "glm (price n/a)",
		"gpt-sol":       "gpt-sol ($1.25 in / $10.00 out per 1M tok)",
	}
	switch {
	case cfg["$schema"] != "https://opencode.ai/config.json", cfg["model"] != "aiven-gateway/claude-sonnet":
		t.Errorf("config = %v", cfg)
	case provider["npm"] != "@ai-sdk/openai-compatible", provider["name"] != "Aiven LLM Gateway":
		t.Errorf("provider = %v", provider)
	case options["baseURL"] != g.URL+"/v1", options["apiKey"] != "{env:AIVEN_GATEWAY_KEY}":
		t.Errorf("options = %v", options)
	case !reflect.DeepEqual(names, wantNames):
		t.Errorf("model names = %v, want %v", names, wantNames)
	}
}

func TestOpencodeDefaultsToFirstAliasWithoutSonnet(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyFlash)
	s.run("opencode")
	if got := opencodeConfig(t, s)["model"]; got != "aiven-gateway/claude-opus" {
		t.Errorf("model = %v", got)
	}
}

func TestOpencodeDefaultsToProductionSonnet(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyProduction)
	s.run("opencode")
	if got := opencodeConfig(t, s)["model"]; got != "aiven-gateway/claude-sonnet-5-5" {
		t.Errorf("model = %v", got)
	}
}

func TestOpencodeForwardsArgs(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.run("opencode", "run", "hi")
	if want := []string{"opencode", "run", "hi"}; !slices.Equal(s.launched().argv, want) {
		t.Errorf("argv = %q, want %q", s.launched().argv, want)
	}
}
