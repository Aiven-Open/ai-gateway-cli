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

	"github.com/aiven-open/ai-gateway-cli/internal/creds"
)

// piModels returns the provider's model entries of the written models.json, in file order.
func piModels(t *testing.T, s *session) []map[string]any {
	t.Helper()
	provider := readJSON(t, piFile(s, "models.json"))["providers"].(map[string]any)["aiven"].(map[string]any)
	var out []map[string]any
	for _, m := range provider["models"].([]any) {
		out = append(out, m.(map[string]any))
	}
	return out
}

func TestPiLaunchesWithGatewayProviderConfig(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	if r := s.setupAndLaunch("pi"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	c := s.launched()
	if c.name != "pi" || !slices.Equal(c.argv, []string{"pi", "--model", "aiven/claude-sonnet"}) {
		t.Fatalf("exec %q %q", c.name, c.argv)
	}
	provider := readJSON(t, piFile(s, "models.json"))["providers"].(map[string]any)["aiven"].(map[string]any)
	if provider["api"] != "openai-completions" || provider["baseUrl"] != g.URL+"/v1" || provider["apiKey"] != "!sh -c '"+creds.Helper(g.URL, runtime.GOOS)+"'" {
		t.Errorf("provider = %v", provider)
	}
	assertUnset(t, c, "PI_CODING_AGENT_DIR", "AIVEN_AI_GATEWAY_KEY")
	want := map[string]map[string]any{
		"aiven-model":   {"api": "openai-completions", "baseUrl": g.URL + "/v1", "contextWindow": 1e6},
		"claude-opus":   {"api": "anthropic-messages", "baseUrl": g.URL, "contextWindow": 1e6},
		"claude-sonnet": {"api": "anthropic-messages", "baseUrl": g.URL, "contextWindow": 1e6, "maxTokens": 64000.0},
		"glm":           {"api": "openai-completions", "baseUrl": g.URL + "/v1", "contextWindow": 1310720.0},
		"gpt-sol":       {"api": "openai-responses", "baseUrl": g.URL + "/v1"},
	}
	var ids []string
	for _, m := range piModels(t, s) {
		id := m["id"].(string)
		ids = append(ids, id)
		if m["api"] != want[id]["api"] || m["baseUrl"] != want[id]["baseUrl"] || m["contextWindow"] != want[id]["contextWindow"] ||
			m["maxTokens"] != want[id]["maxTokens"] || m["reasoning"] != true || !reflect.DeepEqual(m["input"], []any{"text", "image"}) {
			t.Errorf("%s = %v, want %v", id, m, want[id])
		}
	}
	if want := []string{"aiven-model", "claude-opus", "claude-sonnet", "glm", "gpt-sol"}; !slices.Equal(ids, want) {
		t.Errorf("ids = %q, want %q", ids, want)
	}
	if _, err := os.Stat(piFile(s, "settings.json")); !os.IsNotExist(err) {
		t.Errorf("settings.json written: %v", err)
	}
}

func TestPiCostsPerMillionOnlyForPricedModels(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.run("setup", "pi", "--yes")
	byID := map[string]map[string]any{}
	for _, m := range piModels(t, s) {
		byID[m["id"].(string)] = m
	}
	want := map[string]any{"input": 3.0, "output": 15.0, "cacheRead": 0.0, "cacheWrite": 0.0}
	if got := byID["claude-sonnet"]["cost"]; !reflect.DeepEqual(got, want) {
		t.Errorf("claude-sonnet cost = %v, want %v", got, want)
	}
	if got := byID["claude-opus"]["name"]; got != "Claude Opus" {
		t.Errorf("claude-opus name = %v, want the catalogue display name", got)
	}
	for _, id := range []string{"glm", "aiven-model"} {
		if _, has := byID[id]["cost"]; has {
			t.Errorf("%s has cost %v", id, byID[id]["cost"])
		}
	}
}

func TestPiDefaultsToFirstAliasWithoutSonnet(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyFlash)
	s.setupAndLaunch("pi")
	if got := s.launched().argv; !slices.Equal(got, []string{"pi", "--model", "aiven/claude-opus"}) {
		t.Errorf("argv = %q", got)
	}
}

func TestPiDefaultsToProductionSonnet(t *testing.T) {
	for _, key := range []string{keyProduction, keyMixedNames} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(key)
		s.setupAndLaunch("pi")
		if got := s.launched().argv; !slices.Equal(got, []string{"pi", "--model", "aiven/claude-sonnet-5-5"}) {
			t.Errorf("%s: argv = %q", key, got)
		}
	}
}

func TestPiForwardsArgs(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.setupAndLaunch("pi", "-p", "hi")
	if want := []string{"pi", "-p", "hi"}; !slices.Equal(s.launched().argv, want) {
		t.Errorf("argv = %q, want %q", s.launched().argv, want)
	}
}

func piFile(s *session, name string) string { return filepath.Join(s.fakeHome, ".pi", "agent", name) }

func TestPiLeavesTheUsersDefaultAndModelChoiceAlone(t *testing.T) {
	const settings = `{"defaultProvider":"anthropic","defaultModel":"opus"}`
	for _, tc := range []struct{ args, want []string }{
		{[]string{"--model", "anthropic/opus", "hi"}, []string{"pi", "--model", "anthropic/opus", "hi"}},
		{[]string{"--model=aiven/glm"}, []string{"pi", "--model=aiven/glm"}},
		{[]string{"-c", "hi"}, []string{"pi", "-c", "hi"}},
		{[]string{"update"}, []string{"pi", "update"}},
		{[]string{"--", "--model"}, []string{"pi", "--", "--model"}},
	} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
		writeFile(t, piFile(s, "settings.json"), settings)
		if r := s.setupAndLaunch("pi", tc.args...); r.code != 0 {
			t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
		}
		if got := s.launched().argv; !slices.Equal(got, tc.want) {
			t.Errorf("argv = %q, want %q", got, tc.want)
		}
		if data, _ := os.ReadFile(piFile(s, "settings.json")); string(data) != settings {
			t.Errorf("settings.json = %s", data)
		}
	}
}

func TestPiRefusesACredentialSavedForAiven(t *testing.T) {
	const auth = `{"anthropic":{"type":"api_key","key":"sk-ant-other"},"aiven":{"type":"api_key","key":"sk-saved-elsewhere"}}`
	g := newGatewayDouble(t)
	s := newSession(t, g)
	writeFile(t, piFile(s, "auth.json"), auth)
	r := s.stdin(keyValid).run("setup", "pi", "--key-stdin", "--yes")
	if r.code == 0 || !strings.Contains(r.stderr, piFile(s, "auth.json")) || !strings.Contains(r.stderr, "/logout") {
		t.Fatalf("setup: exit %d, stderr %q", r.code, r.stderr)
	}
	if strings.Contains(r.stderr+r.stdout, "sk-saved-elsewhere") {
		t.Error("the saved credential was shown")
	}
	if n := len(g.journal()); n != 0 {
		t.Errorf("setup sent %d gateway requests", n)
	}
	if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
		t.Errorf("key file written: %v", err)
	}
	if _, err := os.Stat(piFile(s, "models.json")); !os.IsNotExist(err) {
		t.Errorf("models.json written: %v", err)
	}

	s = newSession(t, g).loggedIn(keyValid).configured("pi")
	writeFile(t, piFile(s, "auth.json"), auth)
	before := len(g.journal())
	if r := s.run("launch", "pi"); r.code == 0 || !strings.Contains(r.stderr, "/logout") {
		t.Fatalf("launch: exit %d, stderr %q", r.code, r.stderr)
	}
	if len(s.runner.calls) != 0 || len(g.journal()) != before {
		t.Errorf("launch ran pi or reached the gateway: %d calls, %d requests", len(s.runner.calls), len(g.journal())-before)
	}
	if data, _ := os.ReadFile(piFile(s, "auth.json")); string(data) != auth {
		t.Errorf("auth.json changed: %s", data)
	}
}

func TestPiKeepsOtherSavedCredentials(t *testing.T) {
	const auth = `{"anthropic":{"type":"api_key","key":"sk-ant-other"}}`
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	writeFile(t, piFile(s, "auth.json"), auth)
	if r := s.setupAndLaunch("pi"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if data, _ := os.ReadFile(piFile(s, "auth.json")); string(data) != auth {
		t.Errorf("auth.json changed: %s", data)
	}
}
