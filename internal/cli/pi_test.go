// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// piExtensionProvider returns the provider object the generated extension registers.
func piExtensionProvider(t *testing.T, s *session) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.env.Home, "pi", "aiven-gateway.js"))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(data), "const provider = ")
	literal, _, ok2 := strings.Cut(rest, ";\n")
	var p map[string]any
	if !ok || !ok2 || json.Unmarshal([]byte(literal), &p) != nil {
		t.Fatalf("extension has no JSON provider literal:\n%s", data)
	}
	return p
}

// piIsolatedProvider returns the provider of the models.json `pi --isolated` wrote.
func piIsolatedProvider(t *testing.T, s *session) map[string]any {
	t.Helper()
	return readJSON(t, filepath.Join(s.env.Home, "pi", "models.json"))["providers"].(map[string]any)["aiven-gateway"].(map[string]any)
}

// piModels returns the provider's model entries in order.
func piModels(provider map[string]any) []map[string]any {
	var out []map[string]any
	for _, m := range provider["models"].([]any) {
		out = append(out, m.(map[string]any))
	}
	return out
}

func piIDs(provider map[string]any) []string {
	var ids []string
	for _, m := range piModels(provider) {
		ids = append(ids, m["id"].(string))
	}
	return ids
}

// piModelArg returns the `--model` value the launcher passed, or "".
func piModelArg(argv []string) string {
	if i := slices.Index(argv, "--model"); i >= 0 && i+1 < len(argv) {
		return argv[i+1]
	}
	return ""
}

func TestPiLaunchesInOwnProfileWithGatewayExtension(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	if r := s.run("pi"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	c := s.launched()
	ext := filepath.Join(s.env.Home, "pi", "aiven-gateway.js")
	if want := []string{"pi", "-e", ext, "--provider", "aiven-gateway", "--model", "claude-sonnet"}; c.name != "pi" || !slices.Equal(c.argv, want) {
		t.Fatalf("exec %q %q, want %q", c.name, c.argv, want)
	}
	if v, ok := c.envVar("PI_CODING_AGENT_DIR"); ok {
		t.Errorf("PI_CODING_AGENT_DIR = %q, want user's own", v)
	}
	if v, _ := c.envVar("AIVEN_GATEWAY_KEY"); v != keyValid {
		t.Errorf("AIVEN_GATEWAY_KEY = %q", v)
	}
	p := piExtensionProvider(t, s)
	if p["api"] != "openai-completions" || p["baseUrl"] != g.URL+"/v1" || p["apiKey"] != "$AIVEN_GATEWAY_KEY" {
		t.Errorf("provider = %v", p)
	}
	if want := []string{"aiven-model", "claude-opus", "claude-sonnet", "glm", "gpt-sol"}; !slices.Equal(piIDs(p), want) {
		t.Errorf("ids = %q, want %q", piIDs(p), want)
	}
	for _, f := range []string{"models.json", "settings.json"} {
		if _, err := os.Stat(filepath.Join(s.env.Home, "pi", f)); err == nil {
			t.Errorf("wrote %s", f)
		}
	}
}

func TestPiIsolatedUsesPrivateAgentDir(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	if r := s.run("pi", "--isolated", "-p", "hi"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	c := s.launched()
	if !slices.Equal(c.argv, []string{"pi", "-p", "hi"}) {
		t.Fatalf("argv %q", c.argv)
	}
	if v, _ := c.envVar("PI_CODING_AGENT_DIR"); v != filepath.Join(s.env.Home, "pi") {
		t.Errorf("PI_CODING_AGENT_DIR = %q", v)
	}
	p := piIsolatedProvider(t, s)
	if p["api"] != "openai-completions" || p["baseUrl"] != g.URL+"/v1" || p["apiKey"] != "$AIVEN_GATEWAY_KEY" {
		t.Errorf("provider = %v", p)
	}
	if want := []string{"aiven-model", "claude-opus", "claude-sonnet", "glm", "gpt-sol"}; !slices.Equal(piIDs(p), want) {
		t.Errorf("ids = %q, want %q", piIDs(p), want)
	}
	settings := readJSON(t, filepath.Join(s.env.Home, "pi", "settings.json"))
	if want := map[string]any{"defaultProvider": "aiven-gateway", "defaultModel": "claude-sonnet"}; !reflect.DeepEqual(settings, want) {
		t.Errorf("settings = %v, want %v", settings, want)
	}
}

// Extension models get none of the defaults pi's models.json parser fills, so every field is explicit.
func TestPiModelsCarryContextPricesAndPiDefaults(t *testing.T) {
	for _, mode := range []string{"own", "isolated"} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
		provider := piExtensionProvider
		if mode == "isolated" {
			s.run("pi", "--isolated")
			provider = piIsolatedProvider
		} else {
			s.run("pi")
		}
		byID := map[string]map[string]any{}
		for _, m := range piModels(provider(t, s)) {
			byID[m["id"].(string)] = m
		}
		want := map[string]any{
			"id": "claude-sonnet", "name": "claude-sonnet ($3.00 in / $15.00 out per 1M tok)", "reasoning": false, "input": []any{"text"},
			"cost":          map[string]any{"input": 3.0, "output": 15.0, "cacheRead": 0.0, "cacheWrite": 0.0},
			"contextWindow": 1000000.0, "maxTokens": 16384.0,
		}
		if got := byID["claude-sonnet"]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: claude-sonnet = %v, want %v", mode, got, want)
		}
		if got := byID["gpt-sol"]["contextWindow"]; got != 128000.0 {
			t.Errorf("%s: gpt-sol without gateway context: contextWindow = %v, want pi's 128000", mode, got)
		}
		zero := map[string]any{"input": 0.0, "output": 0.0, "cacheRead": 0.0, "cacheWrite": 0.0}
		if got := byID["glm"]["cost"]; !reflect.DeepEqual(got, zero) {
			t.Errorf("%s: unpriced glm cost = %v, want zero", mode, got)
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
	if got := piModelArg(s.launched().argv); got != "claude-opus" {
		t.Errorf("--model = %q", got)
	}
}

func TestPiDefaultsToProductionSonnet(t *testing.T) {
	for _, key := range []string{keyProduction, keyMixedNames} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(key)
		s.run("pi")
		if got := piModelArg(s.launched().argv); got != "claude-sonnet-5-5" {
			t.Errorf("%s: --model = %q", key, got)
		}
	}
}

// pi takes the last --model, so the user's own flags come after the launcher's.
func TestPiForwardsArgsAfterOverlay(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.run("pi", "--model", "glm", "-p", "hi")
	argv := s.launched().argv
	if got, want := argv[len(argv)-4:], []string{"--model", "glm", "-p", "hi"}; !slices.Equal(got, want) {
		t.Errorf("argv = %q, want tail %q", argv, want)
	}
}

// Without --provider/--model, a session pi cannot restore (new, or its model gone) falls back to the user's own default provider.
func TestPiSessionFlagsKeepGatewayDefault(t *testing.T) {
	for _, args := range [][]string{{"-c"}, {"--continue"}, {"-r"}, {"--resume"}, {"--session", "abc"}, {"--session-id", "abc"}, {"--fork", "abc"}, {"--name", "-r"}} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
		s.run(append([]string{"pi"}, args...)...)
		if got := piModelArg(s.launched().argv); got != "claude-sonnet" {
			t.Errorf("%q: --model = %q", args, got)
		}
	}
}

func TestPiRefusesOwnProfileWithoutHome(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.env.PiAgentDir = ""
	r := s.run("pi")
	if want := "aiven-llm: cannot locate your pi profile (HOME unset); run: aiven-llm pi --isolated\n"; r.code != 1 || r.stderr != want {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if r := s.run("pi", "--isolated"); r.code != 0 {
		t.Errorf("--isolated: exit %d, stderr %q", r.code, r.stderr)
	}
}

// --api-key outranks the launcher's key in pi and would sit in argv.
func TestPiRefusesApiKeyArg(t *testing.T) {
	for _, args := range [][]string{{"--api-key", "sk-other"}, {"--api-key=sk-other"}} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
		r := s.run(append([]string{"pi"}, args...)...)
		if want := "aiven-llm: --api-key would replace this login's key; run: aiven-llm login\n"; r.code != 1 || r.stderr != want {
			t.Errorf("%q: exit %d, stderr %q", args, r.code, r.stderr)
		}
		if len(s.runner.calls) != 0 {
			t.Errorf("%q: exec'd %v", args, s.runner.calls)
		}
	}
}

// pi ranks a key in its auth.json above the extension's environment key, so the launcher's login would be ignored.
func TestPiRefusesOwnProfileWithStoredGatewayKey(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	auth := filepath.Join(s.env.PiAgentDir, "auth.json")
	if err := os.MkdirAll(s.env.PiAgentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auth, []byte(`{"aiven-gateway":{"type":"api_key","key":"sk-other"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := s.run("pi")
	if want := "aiven-llm: " + auth + " stores a key for aiven-gateway, which pi would use instead of this login. Remove it with /logout in pi, or run: aiven-llm pi --isolated\n"; r.code != 1 || r.stderr != want {
		t.Fatalf("exit %d, stderr %q, want %q", r.code, r.stderr, want)
	}
	if len(s.runner.calls) != 0 {
		t.Errorf("exec'd %v", s.runner.calls)
	}
	if r := s.run("pi", "--isolated"); r.code != 0 {
		t.Errorf("--isolated: exit %d, stderr %q", r.code, r.stderr)
	}
}

// pi reads its subcommands only as the first argument; they manage the user's own pi and need no provider.
func TestPiSubcommandsGetPlainArgv(t *testing.T) {
	for _, args := range [][]string{{"list"}, {"install", "npm:x"}, {"config"}} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
		if r := s.run(append([]string{"pi"}, args...)...); r.code != 0 {
			t.Fatalf("%q: exit %d, stderr %q", args, r.code, r.stderr)
		}
		if got, want := s.launched().argv, append([]string{"pi"}, args...); !slices.Equal(got, want) {
			t.Errorf("argv = %q, want %q", got, want)
		}
		if _, err := os.Stat(filepath.Join(s.env.Home, "pi")); err == nil {
			t.Errorf("%q: wrote %s", args, filepath.Join(s.env.Home, "pi"))
		}
	}
}
