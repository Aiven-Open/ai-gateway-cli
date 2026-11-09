// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// piModels returns the provider's model entries of the written models.json, in file order.
func piModels(t *testing.T, s *session) []map[string]any {
	t.Helper()
	provider := readJSON(t, filepath.Join(s.env.Home, "pi", "models.json"))["providers"].(map[string]any)["aiven-gateway"].(map[string]any)
	var out []map[string]any
	for _, m := range provider["models"].([]any) {
		out = append(out, m.(map[string]any))
	}
	return out
}

func TestPiLaunchesWithGatewayProviderConfig(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	if r := s.run("pi"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	c := s.launched()
	if c.name != "pi" || !slices.Equal(c.argv, []string{"pi"}) {
		t.Fatalf("exec %q %q", c.name, c.argv)
	}
	if v, _ := c.envVar("PI_CODING_AGENT_DIR"); v != filepath.Join(s.env.Home, "pi") {
		t.Errorf("PI_CODING_AGENT_DIR = %q", v)
	}
	if v, _ := c.envVar("AIVEN_GATEWAY_KEY"); v != keyValid {
		t.Errorf("AIVEN_GATEWAY_KEY = %q", v)
	}
	provider := readJSON(t, filepath.Join(s.env.Home, "pi", "models.json"))["providers"].(map[string]any)["aiven-gateway"].(map[string]any)
	if provider["api"] != "openai-completions" || provider["baseUrl"] != g.URL+"/v1" || provider["apiKey"] != "$AIVEN_GATEWAY_KEY" {
		t.Errorf("provider = %v", provider)
	}
	var ids []string
	for _, m := range piModels(t, s) {
		ids = append(ids, m["id"].(string))
	}
	if want := []string{"aiven-model", "claude-opus", "claude-sonnet", "glm", "gpt-sol"}; !slices.Equal(ids, want) {
		t.Errorf("ids = %q, want %q", ids, want)
	}
	settings := readJSON(t, filepath.Join(s.env.Home, "pi", "settings.json"))
	if want := map[string]any{"defaultProvider": "aiven-gateway", "defaultModel": "claude-sonnet"}; !reflect.DeepEqual(settings, want) {
		t.Errorf("settings = %v, want %v", settings, want)
	}
}

func TestPiCostsPerMillionOnlyForPricedModels(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.run("pi")
	byID := map[string]map[string]any{}
	for _, m := range piModels(t, s) {
		byID[m["id"].(string)] = m
	}
	want := map[string]any{"input": 3.0, "output": 15.0, "cacheRead": 0.0, "cacheWrite": 0.0}
	if got := byID["claude-sonnet"]["cost"]; !reflect.DeepEqual(got, want) {
		t.Errorf("claude-sonnet cost = %v, want %v", got, want)
	}
	if got := byID["claude-sonnet"]["name"]; got != "claude-sonnet ($3.00 in / $15.00 out per 1M tok)" {
		t.Errorf("claude-sonnet name = %v", got)
	}
	for _, id := range []string{"glm", "aiven-model"} {
		if _, has := byID[id]["cost"]; has {
			t.Errorf("%s has cost %v", id, byID[id]["cost"])
		}
	}
}

func TestPiPrintsPriceTableOnStderr(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	r := s.run("pi")
	if want := "claude-sonnet ($3.00 in / $15.00 out per 1M tok)\n"; !strings.Contains(r.stderr, want) {
		t.Errorf("stderr %q lacks %q", r.stderr, want)
	}
}

func TestPiDefaultsToFirstAliasWithoutSonnet(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyFlash)
	s.run("pi")
	got := readJSON(t, filepath.Join(s.env.Home, "pi", "settings.json"))["defaultModel"]
	if got != "claude-opus" {
		t.Errorf("defaultModel = %v", got)
	}
}

func TestPiDefaultsToProductionSonnet(t *testing.T) {
	for _, key := range []string{keyProduction, keyMixedNames} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(key)
		s.run("pi")
		if got := readJSON(t, filepath.Join(s.env.Home, "pi", "settings.json"))["defaultModel"]; got != "claude-sonnet-5-5" {
			t.Errorf("%s: defaultModel = %v", key, got)
		}
	}
}

func TestPiForwardsArgs(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.run("pi", "-p", "hi")
	if want := []string{"pi", "-p", "hi"}; !slices.Equal(s.launched().argv, want) {
		t.Errorf("argv = %q, want %q", s.launched().argv, want)
	}
}
