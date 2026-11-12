// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
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
	r := s.setupAndLaunch("claude")
	if r.code != 0 || r.stderr != launched(s.env.BaseURL, 5, "Claude Code") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	c := s.launched()
	overlay := c.argv[2]
	if c.name != "claude" || !slices.Equal(c.argv, []string{"claude", "--settings", overlay}) {
		t.Fatalf("exec %q %q", c.name, c.argv)
	}
	want := map[string]string{
		"ANTHROPIC_BASE_URL":                         g.URL,
		"ANTHROPIC_AUTH_TOKEN":                       "",
		"ANTHROPIC_API_KEY":                          "",
		"ANTHROPIC_CUSTOM_HEADERS":                   "",
		"CLAUDE_CODE_USE_BEDROCK":                    "",
		"ANTHROPIC_MODEL":                            "glm[1m]",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":               "claude-opus[1m]",
		"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME":          "claude-opus (gateway)",
		"ANTHROPIC_DEFAULT_OPUS_MODEL_DESCRIPTION":   "Claude Opus via Aiven AI Gateway",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":             "glm[1m]",
		"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME":        "glm (gateway)",
		"ANTHROPIC_DEFAULT_SONNET_MODEL_DESCRIPTION": "glm via Aiven AI Gateway",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":              "glm[1m]",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME":         "glm (gateway)",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL_DESCRIPTION":  "glm via Aiven AI Gateway",
		"ANTHROPIC_DEFAULT_FABLE_MODEL":              "",
		"ANTHROPIC_DEFAULT_FABLE_MODEL_NAME":         "",
		"ANTHROPIC_DEFAULT_FABLE_MODEL_DESCRIPTION":  "",
		"ANTHROPIC_CUSTOM_MODEL_OPTION":              "gpt-sol",
		"ANTHROPIC_CUSTOM_MODEL_OPTION_NAME":         "gpt-sol (gateway)",
		"ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION":  "gpt-sol via Aiven AI Gateway",
	}
	for name, v := range want {
		if got, ok := c.envVar(name); !ok || got != v {
			t.Errorf("%s = %q (set %v), want %q", name, got, ok, v)
		}
	}
	assertUnset(t, c, "CLAUDE_CONFIG_DIR")
	assertWindow(t, s, c, "", "")
}

func TestClaudeOverlayCarriesModelAndEnvButNotKey(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	s.setupAndLaunch("claude")
	overlay := launchSettings(t, s.launched())
	wantEnv := map[string]any{
		"ANTHROPIC_BASE_URL":              g.URL,
		"ANTHROPIC_MODEL":                 "glm[1m]",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":    "claude-opus[1m]",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":  "glm[1m]",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":   "glm[1m]",
		"ANTHROPIC_DEFAULT_FABLE_MODEL":   "",
		"ANTHROPIC_CUSTOM_MODEL_OPTION":   "gpt-sol",
		"ANTHROPIC_API_KEY":               "",
		"CLAUDE_CODE_USE_BEDROCK":         "",
		"CLAUDE_CODE_USE_VERTEX":          "",
		"CLAUDE_CODE_USE_FOUNDRY":         "",
		"AWS_BEARER_TOKEN_BEDROCK":        "",
		"ANTHROPIC_CUSTOM_HEADERS":        "",
		"CLAUDE_CODE_MAX_CONTEXT_TOKENS":  "",
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "",
	}
	wantEnv["ANTHROPIC_AUTH_TOKEN"] = ""
	if overlay["model"] != "glm[1m]" || !reflect.DeepEqual(overlay["env"], wantEnv) {
		t.Fatalf("overlay = %v", overlay)
	}
}

func launchSettings(t *testing.T, c execCall) map[string]any {
	t.Helper()
	if len(c.argv) < 3 || c.argv[1] != "--settings" {
		t.Fatalf("no additive settings in %q", c.argv)
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(c.argv[2]), &settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	obj, err := harness.ReadObject(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(harness.JSON(obj), &v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return v
}

func TestClaudeFillsEveryAliasFromClaudeOnlyKey(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyClaudeOnly)
	s.setupAndLaunch("claude")
	c := s.launched()
	assertEnv(t, c, map[string]string{
		"ANTHROPIC_MODEL":                "claude-opus[1m]",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   "claude-opus[1m]",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet[1m]",
		// 200000 is under the 1M floor: no suffix.
		"ANTHROPIC_DEFAULT_HAIKU_MODEL": "claude-haiku",
		"ANTHROPIC_DEFAULT_FABLE_MODEL": "",
		// Every alias is taken, so the custom option falls back to the first one.
		"ANTHROPIC_CUSTOM_MODEL_OPTION": "claude-haiku",
	})
	assertWindow(t, s, c, "", "")
}

func TestClaudeFillsEverySlotFromProductionIds(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyProduction)
	s.setupAndLaunch("claude")
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
	s.setupAndLaunch("claude")
	assertEnv(t, s.launched(), map[string]string{
		"ANTHROPIC_DEFAULT_HAIKU_MODEL": "claude-haiku-4-5",
		"ANTHROPIC_CUSTOM_MODEL_OPTION": "gpt-6-sol",
	})
}

func TestClaudePrefersProductionNamesOverOlderAliases(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyMixedNames)
	s.setupAndLaunch("claude")
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
	s.setupAndLaunch("claude")
	c := s.launched()
	assertEnv(t, c, map[string]string{
		"ANTHROPIC_MODEL":              "deepseek-flash[1m]",
		"ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus[1m]",
	})
	assertWindow(t, s, c, "", "")
}

func TestClaudeFallsBackToRouterOnlyWhenKeyGrantsNothingElse(t *testing.T) {
	cases := []struct{ name, key, main string }{
		{"router alone", keyRouterOnly, "aiven-model[1m]"},
		{"other alias exists", keyRouterSol, "gpt-sol"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(tc.key)
			s.setupAndLaunch("claude")
			c := s.launched()
			assertEnv(t, c, map[string]string{"ANTHROPIC_MODEL": tc.main})
			assertWindow(t, s, c, "", "")
		})
	}
}

func TestClaudeCustomOptionSkipsRouter(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyFlash)
	s.setupAndLaunch("claude")
	assertEnv(t, s.launched(), map[string]string{"ANTHROPIC_CUSTOM_MODEL_OPTION": "deepseek-flash[1m]"})
}

func TestClaudeDeclaresMainWindowWhenUnderOneMillion(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyMidContext)
	s.setupAndLaunch("claude")
	c := s.launched()
	assertEnv(t, c, map[string]string{"ANTHROPIC_MODEL": "glm-fast"})
	assertWindow(t, s, c, "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "262144")
}

func TestClaudeWindowFromProductionMaxInput(t *testing.T) {
	const maxContext, autoCompact = "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "CLAUDE_CODE_AUTO_COMPACT_WINDOW"
	cases := []struct{ name, pin, model, variable, window string }{
		{"1M takes the suffix", "", "claude-sonnet-5-5[1m]", "", ""},
		{"unknown id under 1M", "deepseek-v3.2", "deepseek-v3.2", maxContext, "163840"},
		{"gateway alias Claude Code does not recognise", "claude-haiku-5.5", "claude-haiku-5.5", maxContext, "200000"},
		{"non-Claude id at 200K", "glm-4.7-flash", "glm-4.7-flash", maxContext, "200000"},
		{"recognised id under 1M lowers the window", "claude-opus-4-8", "claude-opus-4-8", autoCompact, "500000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyMaxInput)
			if tc.pin != "" {
				s.writeProjectSettings("settings.json", `{"env":{"ANTHROPIC_MODEL":"`+tc.pin+`"}}`)
			}
			s.setupAndLaunch("claude")
			c := s.launched()
			assertEnv(t, c, map[string]string{"ANTHROPIC_MODEL": tc.model})
			assertWindow(t, s, c, tc.variable, tc.window)
		})
	}
}

// The saved model and a native --model each bring their own window: setup writes the window of the model it keeps,
// and launch declares the window of the model the arguments run.
func TestClaudeWindowFollowsTheChosenModel(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyMaxInput)
	settings := filepath.Join(s.fakeHome, ".claude/aiven.settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"model":"deepseek-v3.2"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s.setupAndLaunch("claude")
	saved := readJSON(t, settings)
	if env, _ := saved["env"].(map[string]any); saved["model"] != "deepseek-v3.2" || env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "163840" {
		t.Errorf("saved model %v with env %v", saved["model"], saved["env"])
	}
	assertWindow(t, s, s.launched(), "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "163840")
	for _, args := range [][]string{{"--model", "claude-opus-4-8"}, {"--model=claude-opus-4-8"}} {
		s.runner.calls = nil
		if r := s.run(append([]string{"launch", "claude", "--"}, args...)...); r.code != 0 {
			t.Fatalf("%q: %s", args, r.stderr)
		}
		assertWindow(t, s, s.launched(), "CLAUDE_CODE_AUTO_COMPACT_WINDOW", "500000")
	}
}

// assertWindow checks that variable alone declares the window in the child env and the overlay, and that the other
// window variable is blank in both; "" wants both blank.
func assertWindow(t *testing.T, s *session, c execCall, variable, want string) {
	t.Helper()
	env := launchSettings(t, c)["env"].(map[string]any)
	for _, name := range []string{"CLAUDE_CODE_MAX_CONTEXT_TOKENS", "CLAUDE_CODE_AUTO_COMPACT_WINDOW"} {
		w := ""
		if name == variable {
			w = want
		}
		got, set := c.envVar(name)
		overlay, inOverlay := env[name]
		if !set || got != w || !inOverlay || overlay != w {
			t.Errorf("%s = %q (set %v), overlay %v (set %v); want %q", name, got, set, overlay, inOverlay, w)
		}
	}
}

// A window variable left in the environment or in Claude Code's own settings would still lower the window of a model
// the other variable declares, so launch blanks it in both the child env and the overlay.
func TestClaudeLaunchBlanksTheInactiveWindow(t *testing.T) {
	for name, inherit := range map[string]func(s *session){
		"environment": func(s *session) {
			s.env.Environ = append(s.env.Environ, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000")
		},
		"settings.json": func(s *session) {
			s.writeUserSettings("settings.json", `{"env":{"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"100000"}}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyMidContext).configured("claude")
			inherit(s)
			if r := s.run("launch", "claude", "--", "--model", "glm-fast"); r.code != 0 {
				t.Fatal(r.stderr)
			}
			assertWindow(t, s, s.launched(), "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "262144")
		})
	}
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
			s.setupAndLaunch("claude")
			// A typed `[1m]` is stripped, then added back only where the context warrants it.
			assertEnv(t, s.launched(), map[string]string{
				"ANTHROPIC_MODEL":                "claude-opus[1m]",
				"ANTHROPIC_DEFAULT_OPUS_MODEL":   "gpt-sol",
				"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet[1m]",
				"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "glm[1m]",
				"ANTHROPIC_DEFAULT_FABLE_MODEL":  "claude-opus[1m]",
			})
			env := launchSettings(t, s.launched())["env"].(map[string]any)
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
	s.setupAndLaunch("claude")
	assertEnv(t, s.launched(), map[string]string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   "claude-opus[1m]",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "gpt-sol",
	})
}

func TestClaudeIgnoresUnusablePins(t *testing.T) {
	cases := []struct{ name, local string }{
		{"non-string pins", `{"env":{"ANTHROPIC_MODEL":5,"ANTHROPIC_DEFAULT_OPUS_MODEL":["gpt-sol"],"ANTHROPIC_DEFAULT_HAIKU_MODEL":null}}`},
		{"blank pin", `{"env":{"ANTHROPIC_MODEL":"","ANTHROPIC_DEFAULT_OPUS_MODEL":"[1m]"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
			s.writeProjectSettings("settings.local.json", tc.local)
			if r := s.setupAndLaunch("claude"); r.code != 0 {
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

func TestClaudeKeyStaysInTheHelperFile(t *testing.T) {
	for _, args := range [][]string{nil, {"-p", "hi"}} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
		s.env.Environ = append(s.env.Environ, "AIVEN_AI_GATEWAY_KEY="+keyFlash, "ANTHROPIC_AUTH_TOKEN="+keyFlash)
		s.setupAndLaunch("claude", args...)
		c := s.launched()
		if helper := launchSettings(t, c)["apiKeyHelper"]; helper != claude.APIKeyHelper(s.env.BaseURL) {
			t.Errorf("helper = %v, want the selected gateway's helper", helper)
		}
		for _, a := range c.argv {
			if strings.Contains(a, keyValid) || strings.Contains(a, keyFlash) {
				t.Errorf("key in argv: %q", c.argv)
			}
		}
		for _, kv := range c.env {
			if strings.Contains(kv, keyValid) || strings.Contains(kv, keyFlash) {
				t.Errorf("key in environment: %q", kv)
			}
		}
		if value, exists := c.envVar("ANTHROPIC_AUTH_TOKEN"); !exists || value != "" {
			t.Errorf("ANTHROPIC_AUTH_TOKEN = %q (set %v), want cleared", value, exists)
		}
		for _, f := range s.homeFiles() {
			if data, _ := os.ReadFile(f); strings.Contains(string(data), keyValid) {
				t.Errorf("key written to %s", f)
			}
		}
	}
}

func TestClaudeChildEnvironmentInheritsAndOverridesOnce(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.env.Environ = append(s.env.Environ, "ANTHROPIC_API_KEY=sk-ant-real", "ANTHROPIC_MODEL=old", "EDITOR=vi")
	s.setupAndLaunch("claude")
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
	if r := s.run("setup", "claude", "--yes"); r.code != 0 {
		t.Fatal(r.stderr)
	}
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(profile, old, old); err != nil {
		t.Fatal(err)
	}
	s.run("launch", "claude")
	s.run("launch", "claude", "--", "-p", "second session")
	info, err := os.Stat(profile)
	if err != nil || !info.ModTime().Equal(old) {
		t.Errorf("profile dir changed: %v, %v", info, err)
	}
	if entries, _ := os.ReadDir(s.fakeHome); len(entries) != 2 || entries[0].Name() != ".claude" || entries[1].Name() != ".config" {
		t.Errorf("HOME entries = %v, want only .claude and the key file's .config", entries)
	}
}

func TestClaudeExecsNothingWhenLaunchCannotProceed(t *testing.T) {
	cases := []struct {
		name, key, want string
	}{
		{"no key", "", "aiven-ai-gateway: no key stored. Run: aiven-ai-gateway login\n"},
		{"gateway rejects key", "sk-bad", catalogueFailed("the gateway rejected the key", "key rejected by gateway (401): Authentication Error. Create a key in Aiven Console and run: aiven-ai-gateway login")},
		{"key grants no models", keyEmpty, catalogueFailed("the key grants no models", "key grants no models. Create a key in Aiven Console and run: aiven-ai-gateway login")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t))
			s.loggedIn(keyValid)
			if r := s.run("setup", "claude", "--yes"); r.code != 0 {
				t.Fatal(r.stderr)
			}
			s.run("logout")
			if tc.key != "" {
				s.loggedIn(tc.key)
			}
			before := configurationSnapshot(t, s.fakeHome, s.env.WorkDir)
			r := s.run("launch", "claude")
			if r.code != 1 || r.stdout != "" || r.stderr != "Gateway: "+s.env.BaseURL+"\n"+tc.want {
				t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
			if len(s.runner.calls) != 0 {
				t.Errorf("exec'd %v", s.runner.calls)
			}
			if after := configurationSnapshot(t, s.fakeHome, s.env.WorkDir); !reflect.DeepEqual(before, after) {
				t.Error("launch changed files")
			}
		})
	}
}

func TestClaudeReportsExecFailure(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	if r := s.run("setup", "claude", "--yes"); r.code != 0 {
		t.Fatal(r.stderr)
	}
	s.deps.Runner = &failingRunner{err: errors.New("claude is required but not installed")}
	r := s.run("launch", "claude")
	if r.code != 1 || r.stderr != launched(s.env.BaseURL, 5, "Claude Code")+"aiven-ai-gateway: claude is required but not installed\n" {
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
	r := s.run("launch", "claude")
	if r.code != 1 || r.stdout != "" || r.stderr != "aiven-ai-gateway: claude is required but not installed; install it, then run aiven-ai-gateway setup claude\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if n := len(g.journal()); n != 0 {
		t.Errorf("gateway got %d requests", n)
	}
	if files := s.homeFiles(); len(files) != 0 {
		t.Errorf("wrote %v", files)
	}
}

func TestClaudeOverlayOutranksUserSettingsForProviderAndSlotEnv(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyFable)
	s.setupAndLaunch("claude")
	env := launchSettings(t, s.launched())["env"].(map[string]any)
	custom, _ := s.launched().envVar("ANTHROPIC_CUSTOM_MODEL_OPTION")
	for k, want := range map[string]string{
		"CLAUDE_CODE_USE_VERTEX":        "",
		"CLAUDE_CODE_USE_FOUNDRY":       "",
		"ANTHROPIC_CUSTOM_HEADERS":      "",
		"ANTHROPIC_DEFAULT_FABLE_MODEL": "claude-opus-5-5[1m]",
		"ANTHROPIC_CUSTOM_MODEL_OPTION": custom,
	} {
		if got, ok := env[k]; !ok || got != want {
			t.Errorf("overlay env %s = %v (present %v), want %q", k, got, ok, want)
		}
	}
}

// writeUserSettings writes <fake home>/.claude/<name> as JSON text.
func (s *session) writeUserSettings(name, text string) {
	s.t.Helper()
	dir := filepath.Join(s.fakeHome, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

func TestClaudeConnectKeepsAChosenModelDeclaresWindowAndRefusesMalformed(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyMidContext)
	if r := s.run("setup", "--key-stdin", "--yes", "claude"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	path := filepath.Join(s.fakeHome, ".claude", "aiven.settings.json")
	got := readJSON(t, path)
	if env, _ := got["env"].(map[string]any); env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "262144" || got["model"] != "glm-fast" {
		t.Errorf("settings = %v", got)
	}
	if _, err := os.Stat(filepath.Join(s.env.WorkDir, ".claude")); !os.IsNotExist(err) {
		t.Errorf("connect claude touched the project: %v", err)
	}
	s.writeUserSettings("aiven.settings.json", `{"model":"claude-opus"}`)
	if r := s.stdin(keyValid).run("setup", "--key-stdin", "claude"); r.code != 0 || readJSON(t, path)["model"] != "claude-opus" {
		t.Errorf("chosen model replaced: exit %d, settings %v", r.code, readJSON(t, path))
	}
	s = newSession(t, newGatewayDouble(t)).stdin(keyValid)
	s.writeUserSettings("aiven.settings.json", `{"env":[]}`)
	if r := s.run("setup", "--key-stdin", "claude"); r.code != 1 || !strings.Contains(r.stderr, "not a JSON object") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
		t.Errorf("key saved despite the bad settings: %v", err)
	}
}

func TestClaudeConnectKeyHelperReadsOnlyTheKeyFile(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	if r := s.run("setup", "--key-stdin", "--yes", "claude"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	helper, _ := readJSON(t, filepath.Join(s.fakeHome, ".claude", "aiven.settings.json"))["apiKeyHelper"].(string)
	if helper != claude.APIKeyHelper(s.env.BaseURL) {
		t.Fatalf("apiKeyHelper = %q, want %q", helper, claude.APIKeyHelper(s.env.BaseURL))
	}
	if runtime.GOOS == "windows" {
		return
	}
	// Claude Code runs the helper through /bin/sh.
	for _, tc := range []struct {
		name, want string
		ok         bool
	}{{"key file", keyValid + "\n", true}, {"replaced key file", keyFlash + "\n", true}, {"environment", "", false}} {
		if tc.name == "replaced key file" {
			s.loggedIn(keyFlash)
		}
		if tc.name == "environment" {
			if err := os.Remove(s.keyPath()); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("/bin/sh", "-c", helper)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + s.fakeHome, "AIVEN_AI_GATEWAY_KEY=" + keyFlash}
		if out, err := cmd.Output(); (err == nil) != tc.ok || string(out) != tc.want {
			t.Errorf("%s: helper printed %q (%v), want %q", tc.name, out, err, tc.want)
		}
	}
}

func TestClaudeConnectWritesGatewaySettingsBesideTheUsersOwn(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).stdin(keyValid)
	const user = `{"model":"opus","env":{"ANTHROPIC_API_KEY":"sk-ant-user"}}`
	const before = `{"permissions":{"allow":["Bash(ls)"]},"env":{"FOO":"bar","CLAUDE_CODE_MAX_CONTEXT_TOKENS":"1"}}`
	s.writeUserSettings("settings.json", user)
	s.writeUserSettings("aiven.settings.json", before)
	path := filepath.Join(s.fakeHome, ".claude", "aiven.settings.json")
	backup := fsutil.BackupPath(path, fixedNow)
	r := s.run("setup", "--key-stdin", "--yes", "claude")
	if r.code != 0 || len(s.runner.calls) != 0 ||
		r.stdout != "" || !strings.Contains(r.stderr, "written: "+path+" (previous version: "+backup+")\n") || !strings.Contains(r.stderr, "directly\n  claude --settings ~/.claude/aiven.settings.json\n") {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if data, _ := os.ReadFile(filepath.Join(s.fakeHome, ".claude", "settings.json")); string(data) != user {
		t.Errorf("settings.json = %s", data)
	}
	data, _ := os.ReadFile(path)
	if bak := latestBackup(t, path); bak != before || strings.Contains(string(data), keyValid) {
		t.Errorf("aiven.settings.json = %s, bak = %s", data, bak)
	}
	got := readJSON(t, path)
	wantEnv := map[string]any{
		"FOO": "bar", "ANTHROPIC_BASE_URL": g.URL, "ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus[1m]",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "glm[1m]", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm[1m]", "ANTHROPIC_DEFAULT_FABLE_MODEL": "",
		"ANTHROPIC_CUSTOM_MODEL_OPTION": "gpt-sol", "ANTHROPIC_AUTH_TOKEN": "", "ANTHROPIC_API_KEY": "", "CLAUDE_CODE_USE_BEDROCK": "",
		"CLAUDE_CODE_USE_VERTEX": "", "CLAUDE_CODE_USE_FOUNDRY": "", "AWS_BEARER_TOKEN_BEDROCK": "", "ANTHROPIC_CUSTOM_HEADERS": "",
		"CLAUDE_CODE_MAX_CONTEXT_TOKENS": "", "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "",
	}
	if !reflect.DeepEqual(got["env"], wantEnv) || got["model"] != "glm[1m]" || !reflect.DeepEqual(got["permissions"], map[string]any{"allow": []any{"Bash(ls)"}}) {
		t.Errorf("settings = %v", got)
	}
	if want := `"apiKeyHelper": "sh -c 'cat ~/.config/aiven/ai-gateway-keys/` + creds.Name(g.URL) + `'"`; runtime.GOOS != "windows" && !strings.Contains(string(data), want) {
		t.Errorf("apiKeyHelper not written as the shell reads it:\n%s", data)
	}
}

// Claude Code assigns settings.local.json's env over settings.json's, so a blank local pin clears the shared one.
func TestClaudeBlankLocalPinClearsTheSharedPin(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.writeProjectSettings("settings.json", `{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"gpt-sol"}}`)
	s.writeProjectSettings("settings.local.json", `{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":""}}`)
	s.setupAndLaunch("claude")
	assertEnv(t, s.launched(), map[string]string{"ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus[1m]"})
}

func TestClaudeForwardsNativeArgsAfterSeparator(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.setupAndLaunch("claude", "-p", "hi", "--model", "chosen-native-model")
	c := s.launched()
	overlay := c.argv[2]
	if want := []string{"claude", "--settings", overlay, "-p", "hi", "--model", "chosen-native-model"}; !slices.Equal(c.argv, want) {
		t.Errorf("argv = %q, want %q", c.argv, want)
	}
	if v, ok := c.envVar("CLAUDE_CONFIG_DIR"); ok {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want unset", v)
	}
}

func TestClaudeLaunchInAnotherProjectDoesNotRewriteSetup(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	first := s.env.WorkDir
	s.setupAndLaunch("claude")
	settings := filepath.Join(s.fakeHome, claude.SettingsFile)
	before, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	s.env.WorkDir = filepath.Join(filepath.Dir(first), "other-project")
	files := configurationSnapshot(t, s.fakeHome, first, s.env.WorkDir)
	s.run("launch", "claude")
	if after, _ := os.ReadFile(settings); string(after) != string(before) {
		t.Errorf("launch in another project rewrote setup:\n%s", after)
	}
	if after := configurationSnapshot(t, s.fakeHome, first, s.env.WorkDir); !reflect.DeepEqual(files, after) {
		t.Error("launch in another project changed files")
	}
}

func TestClaudePinNotGrantedIsUsedByItsID(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.writeProjectSettings("settings.local.json", `{"env":{"ANTHROPIC_DEFAULT_SONNET_MODEL":"ghost-9[1m]"}}`)
	s.setupAndLaunch("claude")
	assertEnv(t, s.launched(), map[string]string{
		"ANTHROPIC_DEFAULT_SONNET_MODEL":             "ghost-9",
		"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME":        "ghost-9 (gateway)",
		"ANTHROPIC_DEFAULT_SONNET_MODEL_DESCRIPTION": "ghost-9 via Aiven AI Gateway",
	})
}

func TestSetupSummaryNotesThatDirectClaudeLoadsExtraSettings(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	r := s.run("setup", "--key-stdin", "--yes", "claude")
	if r.code != 0 || !strings.Contains(r.stderr, "Direct claude --settings also loads extra settings kept in that file;\n") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
}

// Configured first, so only the flag itself can stop the launch.
func TestClaudeRefusesRemovedIsolatedLauncherFlag(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid).configured("claude")
	before, requests := configurationSnapshot(t, s.fakeHome, s.env.WorkDir), len(g.journal())
	r := s.run("launch", "claude", "--isolated")
	if want := "aiven-ai-gateway: launch: name one harness and put native arguments after --. Run: aiven-ai-gateway launch --help\n"; r.code != 1 || r.stderr != want {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if len(s.runner.calls) != 0 || len(g.journal()) != requests || !reflect.DeepEqual(before, configurationSnapshot(t, s.fakeHome, s.env.WorkDir)) {
		t.Fatalf("removed isolation flag exec'd %v, reached the gateway or changed files", s.runner.calls)
	}
}

func TestClaudeFableSlotUsesPublicOpusFallback(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyFable)
	s.setupAndLaunch("claude")
	assertEnv(t, s.launched(), map[string]string{
		"ANTHROPIC_DEFAULT_FABLE_MODEL":             "claude-opus-5-5[1m]",
		"ANTHROPIC_DEFAULT_FABLE_MODEL_NAME":        "claude-opus-5-5 (gateway)",
		"ANTHROPIC_DEFAULT_FABLE_MODEL_DESCRIPTION": "Claude Opus 5.5 via Aiven AI Gateway",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":              "claude-opus-5-5[1m]",
	})
}

// A symlinked project settings file is refused, as every configuration file is, rather than read through.
func TestClaudeLaunchRefusesASymlinkedProjectSettingsFile(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	if r := s.run("setup", "claude", "--yes"); r.code != 0 {
		t.Fatalf("setup: %d %s", r.code, r.stderr)
	}
	target := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, target, `{"model":"claude-opus"}`)
	link := filepath.Join(s.env.WorkDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlink privilege:", err)
	}
	if r := s.run("launch", "claude"); r.code != 1 || !strings.Contains(r.stderr, link+" is a symlink") || len(s.runner.calls) != 0 {
		t.Errorf("exit %d, calls %d: %s", r.code, len(s.runner.calls), r.stderr)
	}
}
