// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Aiven-Open/ai-gateway-cli/internal/harness/claude"
)

// launched fails the test unless exactly one harness was exec'd.
func (s *session) launched() execCall {
	s.t.Helper()
	if len(s.runner.calls) != 1 {
		s.t.Fatalf("exec calls = %d, want 1", len(s.runner.calls))
	}
	return s.runner.calls[0]
}

func TestClaudeLaunchesWithGatewayEnvAndOverlay(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	r := s.run("claude")
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	c := s.launched()
	overlay := claude.OverlayPath(s.genHome(), s.env.WorkDir)
	if c.name != "claude" || !slices.Equal(c.argv, []string{"claude", "--settings", overlay}) {
		t.Fatalf("exec %q %q", c.name, c.argv)
	}
	want := map[string]string{
		"ANTHROPIC_BASE_URL":                         g.URL,
		"AIVEN_GATEWAY_KEY":                          keyValid,
		"ANTHROPIC_AUTH_TOKEN":                       "",
		"ANTHROPIC_API_KEY":                          "",
		"ANTHROPIC_CUSTOM_HEADERS":                   "",
		"CLAUDE_CODE_USE_BEDROCK":                    "",
		"ANTHROPIC_MODEL":                            "glm[1m]",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":               "claude-opus[1m]",
		"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME":          "claude-opus (gateway)",
		"ANTHROPIC_DEFAULT_OPUS_MODEL_DESCRIPTION":   "$15.00 in / $75.00 out per 1M tok",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":             "glm[1m]",
		"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME":        "glm (gateway)",
		"ANTHROPIC_DEFAULT_SONNET_MODEL_DESCRIPTION": "price n/a",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":              "glm[1m]",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME":         "glm (gateway)",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL_DESCRIPTION":  "price n/a",
		"ANTHROPIC_DEFAULT_FABLE_MODEL":              "glm[1m]",
		"ANTHROPIC_DEFAULT_FABLE_MODEL_NAME":         "glm (gateway)",
		"ANTHROPIC_DEFAULT_FABLE_MODEL_DESCRIPTION":  "price n/a",
		"ANTHROPIC_CUSTOM_MODEL_OPTION":              "gpt-sol",
		"ANTHROPIC_CUSTOM_MODEL_OPTION_NAME":         "gpt-sol (gateway)",
		"ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION":  "$1.25 in / $10.00 out per 1M tok",
	}
	for name, v := range want {
		if got, ok := c.envVar(name); !ok || got != v {
			t.Errorf("%s = %q (set %v), want %q", name, got, ok, v)
		}
	}
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CLAUDE_CODE_AUTO_COMPACT_WINDOW"} {
		if v, ok := c.envVar(name); ok {
			t.Errorf("%s = %q, want unset", name, v)
		}
	}
}

// Settings `env` beats the process environment, so the overlay (the highest layer below managed settings) blanks
// the user's own token and headers and hands Claude Code the key through `apiKeyHelper`, which reads it from env.
func TestClaudeOverlayCarriesModelAndEnvButNotKey(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	s.run("claude")
	overlay := readJSON(t, claude.OverlayPath(s.genHome(), s.env.WorkDir))
	wantEnv := map[string]any{
		"ANTHROPIC_BASE_URL":             g.URL,
		"ANTHROPIC_MODEL":                "glm[1m]",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   "claude-opus[1m]",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "glm[1m]",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "glm[1m]",
		"ANTHROPIC_DEFAULT_FABLE_MODEL":  "glm[1m]",
		"ANTHROPIC_CUSTOM_MODEL_OPTION":  "gpt-sol",
		"ANTHROPIC_API_KEY":              "",
		"ANTHROPIC_AUTH_TOKEN":           "",
		"ANTHROPIC_CUSTOM_HEADERS":       "",
		"CLAUDE_CODE_USE_BEDROCK":        "",
		"CLAUDE_CODE_USE_VERTEX":         "",
		"CLAUDE_CODE_USE_FOUNDRY":        "",
		"AWS_BEARER_TOKEN_BEDROCK":       "",
	}
	if overlay["apiKeyHelper"] != "printenv AIVEN_GATEWAY_KEY" {
		t.Errorf("apiKeyHelper = %v", overlay["apiKeyHelper"])
	}
	if overlay["model"] != "glm[1m]" || !reflect.DeepEqual(overlay["env"], wantEnv) {
		t.Fatalf("overlay = %v", overlay)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return v
}

func TestClaudeIsolatedUsesBlankProfileAndIsConsumed(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.run("claude", "--isolated", "--foo")
	c := s.launched()
	if got, _ := c.envVar("CLAUDE_CONFIG_DIR"); got != filepath.Join(s.genHome(), "claude") {
		t.Errorf("CLAUDE_CONFIG_DIR = %q", got)
	}
	overlay := claude.OverlayPath(s.genHome(), s.env.WorkDir)
	if want := []string{"claude", "--settings", overlay, "--foo"}; !slices.Equal(c.argv, want) {
		t.Errorf("argv = %q, want %q", c.argv, want)
	}
}

func TestClaudeForwardsArgsAndRecognisesIsolatedOnlyFirst(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.run("claude", "-p", "hi", "--isolated")
	c := s.launched()
	overlay := claude.OverlayPath(s.genHome(), s.env.WorkDir)
	if want := []string{"claude", "--settings", overlay, "-p", "hi", "--isolated"}; !slices.Equal(c.argv, want) {
		t.Errorf("argv = %q, want %q", c.argv, want)
	}
	if v, ok := c.envVar("CLAUDE_CONFIG_DIR"); ok {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want unset", v)
	}
}

func TestClaudeFillsEveryAliasFromClaudeOnlyKey(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyClaudeOnly)
	s.run("claude")
	c := s.launched()
	assertEnv(t, c, map[string]string{
		"ANTHROPIC_MODEL":                "claude-opus[1m]",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   "claude-opus[1m]",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet[1m]",
		// 200000 is under the 1M floor for sub-aliases: no suffix.
		"ANTHROPIC_DEFAULT_HAIKU_MODEL": "claude-haiku",
		"ANTHROPIC_DEFAULT_FABLE_MODEL": "claude-opus[1m]",
		// Every alias is taken, so the custom option falls back to the first one.
		"ANTHROPIC_CUSTOM_MODEL_OPTION": "claude-haiku",
	})
	assertUnset(t, c, "CLAUDE_CODE_AUTO_COMPACT_WINDOW")
	if _, ok := readJSON(t, claude.OverlayPath(s.genHome(), s.env.WorkDir))["env"].(map[string]any)["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; ok {
		t.Error("overlay sets CLAUDE_CODE_AUTO_COMPACT_WINDOW")
	}
}

func TestClaudeFillsEverySlotFromProductionIds(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyProduction)
	s.run("claude")
	assertEnv(t, s.launched(), map[string]string{
		"ANTHROPIC_MODEL":                "claude-sonnet-5-5",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   "claude-opus-5-5",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-5-5",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "claude-haiku-5.5",
		"ANTHROPIC_DEFAULT_FABLE_MODEL":  "claude-opus-5-5",
		"ANTHROPIC_CUSTOM_MODEL_OPTION":  "gpt-6.1-sol",
	})
}

func TestClaudeFallsBackWithinProductionIds(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyProductionFallback)
	s.run("claude")
	assertEnv(t, s.launched(), map[string]string{
		"ANTHROPIC_DEFAULT_HAIKU_MODEL": "claude-haiku-4-5",
		"ANTHROPIC_CUSTOM_MODEL_OPTION": "gpt-6-sol",
	})
}

func TestClaudePrefersProductionNamesOverOlderAliases(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyMixedNames)
	s.run("claude")
	assertEnv(t, s.launched(), map[string]string{
		"ANTHROPIC_MODEL":                "claude-sonnet-5-5",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-5-5",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "claude-haiku-5-5",
	})
}

func assertEnv(t *testing.T, c execCall, want map[string]string) {
	t.Helper()
	for name, v := range want {
		if got, ok := c.envVar(name); !ok || got != v {
			t.Errorf("%s = %q (set %v), want %q", name, got, ok, v)
		}
	}
}

func assertUnset(t *testing.T, c execCall, names ...string) {
	t.Helper()
	for _, name := range names {
		if v, ok := c.envVar(name); ok {
			t.Errorf("%s = %q, want unset", name, v)
		}
	}
}

func TestClaudeMainPrefersFlashOverRouter(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyFlash)
	s.run("claude")
	c := s.launched()
	assertEnv(t, c, map[string]string{
		"ANTHROPIC_MODEL":              "deepseek-flash[1m]",
		"ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus[1m]",
	})
	assertUnset(t, c, "CLAUDE_CODE_AUTO_COMPACT_WINDOW")
}

func TestClaudeFallsBackToRouterOnlyWhenKeyGrantsNothingElse(t *testing.T) {
	cases := []struct{ name, key, main string }{
		{"router alone", keyRouterOnly, "aiven-model[1m]"},
		{"other alias exists", keyRouterSol, "gpt-sol"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(tc.key)
			s.run("claude")
			c := s.launched()
			assertEnv(t, c, map[string]string{"ANTHROPIC_MODEL": tc.main})
			assertUnset(t, c, "CLAUDE_CODE_AUTO_COMPACT_WINDOW")
		})
	}
}

func TestClaudeCustomOptionSkipsRouter(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyFlash)
	s.run("claude")
	assertEnv(t, s.launched(), map[string]string{"ANTHROPIC_CUSTOM_MODEL_OPTION": "deepseek-flash[1m]"})
}

func TestClaudeCompactsAtMainWindowWhenUnderOneMillion(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyMidContext)
	s.run("claude")
	assertEnv(t, s.launched(), map[string]string{
		"ANTHROPIC_MODEL":                 "glm-fast[1m]",
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "262144",
	})
	env := readJSON(t, claude.OverlayPath(s.genHome(), s.env.WorkDir))["env"].(map[string]any)
	if env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] != "262144" {
		t.Errorf("overlay env = %v", env)
	}
}

func TestClaudeFableSlotFollowsItsLadder(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyFable)
	s.run("claude")
	assertEnv(t, s.launched(), map[string]string{
		"ANTHROPIC_DEFAULT_FABLE_MODEL":             "claude-fable[1m]",
		"ANTHROPIC_DEFAULT_FABLE_MODEL_NAME":        "claude-fable (gateway)",
		"ANTHROPIC_DEFAULT_FABLE_MODEL_DESCRIPTION": "$10.00 in / $50.00 out per 1M tok",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":              "claude-fable[1m]",
	})
}

// writeProjectSettings writes <project>/.claude/<name> with the given env pins as JSON text.
func (s *session) writeProjectSettings(name, text string) {
	s.t.Helper()
	dir := filepath.Join(s.env.WorkDir, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

const pinsAll = `{"env":{
	"ANTHROPIC_MODEL":"claude-opus[1m]",
	"ANTHROPIC_DEFAULT_OPUS_MODEL":"gpt-sol",
	"ANTHROPIC_DEFAULT_SONNET_MODEL":"claude-sonnet[1m]",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL":"glm",
	"ANTHROPIC_DEFAULT_FABLE_MODEL":"claude-opus"}}`

func TestClaudeAdoptsProjectPinsAndRecomputesSuffix(t *testing.T) {
	for _, file := range []string{"settings.local.json", "settings.json"} {
		t.Run(file, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
			s.writeProjectSettings(file, pinsAll)
			if r := s.run("claude"); r.stderr != "" {
				t.Errorf("stderr %q", r.stderr)
			}
			// A typed `[1m]` is stripped, then added back only where the context warrants it.
			assertEnv(t, s.launched(), map[string]string{
				"ANTHROPIC_MODEL":                "claude-opus[1m]",
				"ANTHROPIC_DEFAULT_OPUS_MODEL":   "gpt-sol",
				"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet[1m]",
				"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "glm[1m]",
				"ANTHROPIC_DEFAULT_FABLE_MODEL":  "claude-opus[1m]",
			})
			env := readJSON(t, claude.OverlayPath(s.genHome(), s.env.WorkDir))["env"].(map[string]any)
			if env["ANTHROPIC_MODEL"] != "claude-opus[1m]" || env["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "gpt-sol" || env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "glm[1m]" {
				t.Errorf("overlay env = %v", env)
			}
		})
	}
}

func TestClaudeLocalProjectPinsWinOverSharedPerKey(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.writeProjectSettings("settings.json", `{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"claude-sonnet","ANTHROPIC_DEFAULT_SONNET_MODEL":"gpt-sol"}}`)
	s.writeProjectSettings("settings.local.json", `{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"claude-opus"}}`)
	s.run("claude")
	assertEnv(t, s.launched(), map[string]string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   "claude-opus[1m]",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "gpt-sol",
	})
}

func TestClaudeIgnoresUnusablePins(t *testing.T) {
	cases := []struct{ name, local string }{
		{"invalid JSON", `{not json`},
		{"env not an object", `{"env":"x"}`},
		{"non-string pins", `{"env":{"ANTHROPIC_MODEL":5,"ANTHROPIC_DEFAULT_OPUS_MODEL":["gpt-sol"],"ANTHROPIC_DEFAULT_HAIKU_MODEL":null}}`},
		{"blank pin", `{"env":{"ANTHROPIC_MODEL":"","ANTHROPIC_DEFAULT_OPUS_MODEL":"[1m]"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
			s.writeProjectSettings("settings.local.json", tc.local)
			if r := s.run("claude"); r.code != 0 {
				t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
			}
			assertEnv(t, s.launched(), map[string]string{
				"ANTHROPIC_MODEL":               "glm[1m]",
				"ANTHROPIC_DEFAULT_OPUS_MODEL":  "claude-opus[1m]",
				"ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm[1m]",
			})
		})
	}
}

func TestClaudeFallsBackPastUnusableLocalToSharedPins(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.writeProjectSettings("settings.json", `{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"gpt-sol"}}`)
	s.writeProjectSettings("settings.local.json", `{not json`)
	s.run("claude")
	assertEnv(t, s.launched(), map[string]string{"ANTHROPIC_DEFAULT_OPUS_MODEL": "gpt-sol"})
}

func TestClaudeWarnsAndKeepsDefaultForPinsNotGranted(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.writeProjectSettings("settings.local.json", `{"model":"opusplan","env":{"ANTHROPIC_DEFAULT_SONNET_MODEL":"ghost-9[1m]"}}`)
	r := s.run("claude")
	assertEnv(t, s.launched(), map[string]string{
		"ANTHROPIC_MODEL":                     "glm[1m]",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":      "glm[1m]",
		"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME": "glm (gateway)",
	})
	for _, want := range []string{
		`aiven-llm: project setting ANTHROPIC_DEFAULT_SONNET_MODEL "ghost-9" is not granted to your key; using glm`,
		`aiven-llm: project setting model "opusplan" is not granted to your key; using glm`,
	} {
		if !strings.Contains(r.stderr, want+"\n") {
			t.Errorf("stderr %q lacks %q", r.stderr, want)
		}
	}
}

func TestClaudeAdoptsProjectModelSetting(t *testing.T) {
	cases := []struct{ name, shared, local, main string }{
		{"shared", `{"model":"gpt-sol"}`, ``, "gpt-sol"},
		{"local over shared", `{"model":"gpt-sol"}`, `{"model":"claude-sonnet"}`, "claude-sonnet[1m]"},
		{"env pin over setting in any file", `{"env":{"ANTHROPIC_MODEL":"gpt-sol"}}`, `{"model":"claude-sonnet"}`, "gpt-sol"},
		{"claude alias resolves through slot", `{"model":"opus"}`, ``, "claude-opus[1m]"},
		{"alias with suffix", `{"model":"sonnet[1m]"}`, ``, "glm[1m]"},
		{"alias follows pinned slot", `{"model":"opus","env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"gpt-sol"}}`, ``, "gpt-sol"},
		{"fable alias", `{"model":"fable"}`, ``, "glm[1m]"},
		{"default keeps launcher choice", `{"model":"default"}`, ``, "glm[1m]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
			s.writeProjectSettings("settings.json", tc.shared)
			if tc.local != "" {
				s.writeProjectSettings("settings.local.json", tc.local)
			}
			r := s.run("claude")
			if r.code != 0 || r.stderr != "" {
				t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
			}
			assertEnv(t, s.launched(), map[string]string{"ANTHROPIC_MODEL": tc.main})
			if got := readJSON(t, claude.OverlayPath(s.genHome(), s.env.WorkDir))["model"]; got != tc.main {
				t.Errorf("overlay model = %v, want %q", got, tc.main)
			}
		})
	}
}

func TestClaudeKeyReachesOnlyChildEnvironment(t *testing.T) {
	for _, args := range [][]string{{"claude"}, {"claude", "--isolated", "-p", "hi"}} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
		s.run(args...)
		c := s.launched()
		for _, a := range c.argv {
			if strings.Contains(a, keyValid) {
				t.Errorf("key in argv: %q", c.argv)
			}
		}
		var holders []string
		for _, kv := range c.env {
			if strings.Contains(kv, keyValid) {
				holders = append(holders, kv)
			}
		}
		if want := []string{"AIVEN_GATEWAY_KEY=" + keyValid}; !slices.Equal(holders, want) {
			t.Errorf("env entries holding the key = %q, want %q", holders, want)
		}
		for _, f := range s.homeFiles() {
			if filepath.Dir(f) == "aig-keys" {
				continue
			}
			if data, _ := os.ReadFile(filepath.Join(s.env.Home, f)); strings.Contains(string(data), keyValid) {
				t.Errorf("key written to %s", f)
			}
		}
	}
}

func TestClaudeChildEnvironmentInheritsAndOverridesOnce(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.env.Environ = append(s.env.Environ, "ANTHROPIC_API_KEY=sk-ant-real", "ANTHROPIC_MODEL=old", "EDITOR=vi")
	s.run("claude")
	c := s.launched()
	if v, _ := c.envVar("EDITOR"); v != "vi" {
		t.Errorf("EDITOR = %q, want inherited", v)
	}
	if v, _ := c.envVar("PATH"); v != "/usr/bin:/bin" {
		t.Errorf("PATH = %q, want inherited", v)
	}
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_MODEL"} {
		n := 0
		for _, kv := range c.env {
			if strings.HasPrefix(kv, name+"=") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s appears %d times in env", name, n)
		}
	}
	if v, _ := c.envVar("ANTHROPIC_API_KEY"); v != "" {
		t.Errorf("ANTHROPIC_API_KEY = %q, want blanked", v)
	}
}

func TestClaudeLeavesDefaultProfileAlone(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	profile := filepath.Join(s.fakeHome, ".claude")
	if err := os.Mkdir(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(profile, old, old); err != nil {
		t.Fatal(err)
	}
	s.run("claude")
	s.run("claude", "--isolated")
	info, err := os.Stat(profile)
	if err != nil || !info.ModTime().Equal(old) {
		t.Errorf("profile dir changed: %v, %v", info, err)
	}
	if entries, _ := os.ReadDir(s.fakeHome); len(entries) != 1 {
		t.Errorf("HOME entries = %v, want only .claude", entries)
	}
}

func TestClaudeExecsNothingWhenLaunchCannotProceed(t *testing.T) {
	cases := []struct {
		name, key, want string
	}{
		{"no key", "", "aiven-llm: no key stored. Run: aiven-llm login\n"},
		{"gateway rejects key", "sk-bad", "aiven-llm: key rejected by gateway (401). Create a key in Aiven Console and run: aiven-llm login\n"},
		{"key grants no models", keyEmpty, "aiven-llm: key grants no models. Create a key in Aiven Console and run: aiven-llm login\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t))
			if tc.key != "" {
				s.loggedIn(tc.key)
			}
			r := s.run("claude")
			if r.code != 1 || r.stdout != "" || r.stderr != tc.want {
				t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
			if len(s.runner.calls) != 0 {
				t.Errorf("exec'd %v", s.runner.calls)
			}
			for _, f := range s.homeFiles() {
				if filepath.Dir(f) != "aig-keys" {
					t.Errorf("wrote %s", f)
				}
			}
		})
	}
}

func TestClaudeReportsExecFailure(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.deps.Runner = &failingRunner{err: errors.New("claude is required but not installed")}
	r := s.run("claude")
	if r.code != 1 || r.stderr != "aiven-llm: claude is required but not installed\n" {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
}

type failingRunner struct {
	fakeRunner
	err error
}

func (f failingRunner) Exec(string, []string, []string) error { return f.err }

func TestClaudeMissingBinaryFailsBeforeGatewayOrWrites(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	s.runner.missing = []string{"claude"}
	r := s.run("claude")
	if r.code != 1 || r.stdout != "" || r.stderr != "aiven-llm: claude is required but not installed\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if n := len(g.journal()); n != 0 {
		t.Errorf("gateway got %d requests", n)
	}
	for _, f := range s.homeFiles() {
		if filepath.Dir(f) != "aig-keys" {
			t.Errorf("wrote %s", f)
		}
	}
}

func TestClaudeOverlayIsPerProject(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	first := s.env.WorkDir
	s.run("claude")
	before, err := os.ReadFile(claude.OverlayPath(s.genHome(), first))
	if err != nil {
		t.Fatal(err)
	}
	s.env.WorkDir = filepath.Join(filepath.Dir(first), "other-project")
	s.run("claude")
	if claude.OverlayPath(s.genHome(), first) == claude.OverlayPath(s.genHome(), s.env.WorkDir) {
		t.Fatal("two projects share one overlay path")
	}
	if after, _ := os.ReadFile(claude.OverlayPath(s.genHome(), first)); string(after) != string(before) {
		t.Errorf("launch in another project rewrote the first project's overlay:\n%s", after)
	}
}

func TestClaudeOverlayOutranksUserSettingsForProviderAndSlotEnv(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyFable)
	s.run("claude")
	env := readJSON(t, claude.OverlayPath(s.genHome(), s.env.WorkDir))["env"].(map[string]any)
	custom, _ := s.launched().envVar("ANTHROPIC_CUSTOM_MODEL_OPTION")
	for k, want := range map[string]string{
		"CLAUDE_CODE_USE_VERTEX":        "",
		"CLAUDE_CODE_USE_FOUNDRY":       "",
		"ANTHROPIC_DEFAULT_FABLE_MODEL": "claude-fable[1m]",
		"ANTHROPIC_CUSTOM_MODEL_OPTION": custom,
	} {
		if got, ok := env[k]; !ok || got != want {
			t.Errorf("overlay env %s = %v (present %v), want %q", k, got, ok, want)
		}
	}
}
