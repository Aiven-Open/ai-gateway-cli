// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
)

const (
	keyOpenWeightLA    = "sk-test-open-weight-la"
	keyProductionOwned = "sk-test-production-owned"
	keyGeminiFour      = "sk-test-gemini-four"
)

func init() {
	keyModels[keyGeminiFour] = []string{
		`{"id":"gemini-4-argon","object":"model","created":0,"owned_by":"Google"}`,
		`{"id":"gemini-4-carbon","object":"model","created":0,"owned_by":"Google"}`,
		`{"id":"gemini-4-hydrogen","object":"model","created":0,"owned_by":"Google"}`,
		`{"id":"gemini-4-neon","object":"model","created":0,"owned_by":"Google"}`,
	}
	keyModels[keyOpenWeightLA] = []string{
		`{"id":"deepseek-v3.2","object":"model","created":0,"owned_by":"DeepSeek","context_window_tokens":163840}`,
		`{"id":"devstral-2-123b","object":"model","created":0,"owned_by":"Mistral AI","context_window_tokens":262144}`,
		`{"id":"glm-4.7-flash","object":"model","created":0,"owned_by":"Z.ai","context_window_tokens":200000}`,
		`{"id":"glm-5","object":"model","created":0,"owned_by":"Z.ai","context_window_tokens":1000000}`,
		`{"id":"gpt-oss-120b","object":"model","created":0,"owned_by":"OpenAI","context_window_tokens":131072}`,
		`{"id":"qwen3-coder-480b","object":"model","created":0,"owned_by":"Alibaba","context_window_tokens":262144}`,
	}
	keyModels[keyProductionOwned] = []string{
		`{"id":"claude-haiku-4-5","object":"model","created":0,"owned_by":"Anthropic"}`,
		`{"id":"claude-haiku-5.5","object":"model","created":0,"owned_by":"Anthropic"}`,
		`{"id":"claude-opus-5-5","object":"model","created":0,"owned_by":"Anthropic","context_window_tokens":1000000}`,
		`{"id":"claude-sonnet-5-5","object":"model","created":0,"owned_by":"Anthropic","context_window_tokens":1000000}`,
		`{"id":"deepseek-v3.2","object":"model","created":0,"owned_by":"DeepSeek"}`,
		`{"id":"gemini-3.8-flash","object":"model","created":0,"owned_by":"Google","context_window_tokens":1048576}`,
		`{"id":"gemini-4-argon","object":"model","created":0,"owned_by":"Google","context_window_tokens":1048576}`,
		`{"id":"glm-4.7-flash","object":"model","created":0,"owned_by":"Z.ai"}`,
		`{"id":"glm-5","object":"model","created":0,"owned_by":"Z.ai"}`,
		`{"id":"gpt-5.4","object":"model","created":0,"owned_by":"OpenAI"}`,
		`{"id":"gpt-6.1-sol","object":"model","created":0,"owned_by":"OpenAI"}`,
	}
}

func recordProbes(t *testing.T, g *gatewayDouble) func() map[string][]string {
	t.Helper()
	var mu sync.Mutex
	probes := map[string][]string{}
	base := g.Config.Handler
	g.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			mu.Lock()
			probes[r.URL.Path] = append(probes[r.URL.Path], body.Model)
			mu.Unlock()
		}
		base.ServeHTTP(w, r)
	})
	return func() map[string][]string {
		mu.Lock()
		defer mu.Unlock()
		out := make(map[string][]string, len(probes))
		for k, v := range probes {
			out[k] = append([]string(nil), v...)
		}
		return out
	}
}

// End-to-end setup -> launch persistence test through cli.Run: choosing glm-5 (Open-weight preset) during setup
// with direct invocation OFF causes subsequent `launch pi|opencode|omp` to inject aiven/glm-5, lets explicit
// --model/-m args override, and falls back cleanly when the persisted alias is no longer granted.
func TestSetupToLaunchPersistsChosenModelWithoutNativeDefault(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tool, flag, nativeDefaultFile string
	}{
		{"pi", "--model", ".pi/agent/settings.json"},
		{"opencode", "-m", ".config/opencode/opencode.json"},
		{"omp", "--model", ".omp/agent/config.yml"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()
			g := newGatewayDouble(t)
			s := newSession(t, g).stdin(keyProductionOwned)
			// Beat 2: 1=Anthropic, 2=OpenAI, 3=Gemini, 4=Open-weight (deepseek-v3.2); Beat 3: 2=No (launch only); Review: y=Apply.
			s.deps.Prompter = &scriptedPrompter{answers: []string{"4", "2", "y"}}
			r := s.run("setup", tc.tool, "--key-stdin", "--plain")
			if r.code != 0 {
				t.Fatalf("setup %s: exit %d, stderr:\n%s", tc.tool, r.code, r.stderr)
			}

			// Verify ai-gateway-models.json persisted deepseek-v3.2 with mode 0600 on Unix.
			prefsPath := config.ModelPrefsPath(s.fakeHome)
			if runtime.GOOS != "windows" {
				info, err := os.Stat(prefsPath)
				if err != nil {
					t.Fatalf("prefs file missing: %v", err)
				}
				if info.Mode().Perm() != 0o600 {
					t.Errorf("prefs mode = %o, want 0600", info.Mode().Perm())
				}
			}
			gotPref, err := config.LoadModelPref(s.fakeHome, s.env.BaseURL, tc.tool)
			if err != nil || gotPref != "deepseek-v3.2" {
				t.Fatalf("LoadModelPref = %q, %v; want deepseek-v3.2", gotPref, err)
			}
			// Direct invocation stayed OFF.
			if tc.tool != "opencode" {
				if _, err := os.Stat(filepath.Join(s.fakeHome, tc.nativeDefaultFile)); !os.IsNotExist(err) {
					t.Errorf("native default %s written despite No", tc.nativeDefaultFile)
				}
			} else {
				cfg := readJSON(t, filepath.Join(s.fakeHome, tc.nativeDefaultFile))
				if _, hasModel := cfg["model"]; hasModel {
					t.Errorf("opencode.json set model despite No: %v", cfg["model"])
				}
			}

			// 1. launch without args must inject aiven/deepseek-v3.2!
			if lr := s.run("launch", tc.tool); lr.code != 0 {
				t.Fatalf("launch %s: exit %d, stderr:\n%s", tc.tool, lr.code, lr.stderr)
			}
			if got := s.runner.calls[0].argv; !slices.Equal(
				got,
				[]string{tc.tool, tc.flag, "aiven/deepseek-v3.2"},
			) {
				t.Fatalf("launch argv = %v, want [%s %s aiven/deepseek-v3.2]", got, tc.tool, tc.flag)
			}

			// 1b. launch with non-model native args also injects aiven/deepseek-v3.2 (R1)!
			var extraArgs, wantExtraArgv []string
			switch tc.tool {
			case "pi":
				extraArgs = []string{"--continue"}
				wantExtraArgv = []string{"pi", "--model", "aiven/deepseek-v3.2", "--continue"}
			case "omp":
				extraArgs = []string{"-p", "hi"}
				wantExtraArgv = []string{"omp", "--model", "aiven/deepseek-v3.2", "-p", "hi"}
			case "opencode":
				extraArgs = []string{"run", "--pure", "hi"}
				wantExtraArgv = []string{"opencode", "run", "-m", "aiven/deepseek-v3.2", "--pure", "hi"}
			}
			if lr := s.run(append([]string{"launch", tc.tool, "--"}, extraArgs...)...); lr.code != 0 {
				t.Fatalf("launch extra args %s: exit %d, stderr:\n%s", tc.tool, lr.code, lr.stderr)
			}
			if got := s.runner.calls[1].argv; !slices.Equal(got, wantExtraArgv) {
				t.Fatalf("extra args argv = %v, want %v", got, wantExtraArgv)
			}

			// 2. Explicit native --model/-m after -- still overrides.
			if lr := s.run("launch", tc.tool, "--", tc.flag, "aiven/glm-5"); lr.code != 0 {
				t.Fatalf("launch override: exit %d, stderr:\n%s", lr.code, lr.stderr)
			}
			if got := s.runner.calls[2].argv; !slices.Equal(
				got,
				[]string{tc.tool, tc.flag, "aiven/glm-5"},
			) {
				t.Fatalf("override argv = %v", got)
			}

			// 2b. Meta flags (--help) and non-session subcommands (opencode models) pass through unchanged.
			if lr := s.run("launch", tc.tool, "--", "--help"); lr.code != 0 {
				t.Fatalf("launch --help: exit %d, stderr:\n%s", lr.code, lr.stderr)
			}
			if got := s.runner.calls[3].argv; !slices.Equal(got, []string{tc.tool, "--help"}) {
				t.Fatalf("--help argv = %v", got)
			}

			// 3. When key no longer grants glm-5 (keyClaudeOnly grants claude-sonnet/opus/haiku), launch falls back cleanly.
			s.loggedIn(keyClaudeOnly)
			// Refresh tool catalogue so claude-sonnet exists in models.json/opencode.json/models.yml.
			if rr := s.run("setup", tc.tool, "--yes"); rr.code != 0 {
				t.Fatalf("refresh setup: exit %d, stderr:\n%s", rr.code, rr.stderr)
			}
			if lr := s.run("launch", tc.tool); lr.code != 0 {
				t.Fatalf("launch fallback: exit %d, stderr:\n%s", lr.code, lr.stderr)
			}
			if got := s.runner.calls[4].argv; !slices.Equal(
				got,
				[]string{tc.tool, tc.flag, "aiven/claude-sonnet"},
			) {
				t.Fatalf("fallback argv = %v, want claude-sonnet", got)
			}
		})
	}
}

func TestSetupPlainFreshOpenWeightPresetOnLAKey(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"claude", "codex", "pi", "opencode", "omp"} {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			g := newGatewayDouble(t)
			probes := recordProbes(t, g)
			s := newSession(t, g).stdin(keyOpenWeightLA)
			// Beat 2: Enter (1=Open-weight), Beat 3: n (launch only), Review: y (apply).
			s.deps.Prompter = &scriptedPrompter{answers: []string{"", "n", "y"}}
			r := s.run("setup", tool, "--key-stdin", "--plain")
			if r.code != 0 {
				t.Fatalf("exit %d, stderr %s", r.code, r.stderr)
			}
			first := "1) Open-weight (deepseek-v3.2"
			if tool == "claude" {
				first = "1) Recommended defaults (deepseek-v3.2)"
			}
			if !strings.Contains(r.stderr, first) {
				t.Fatalf("expected %q as default option 1:\n%s", first, r.stderr)
			}
			gotProbes := probes()
			switch tool {
			case "claude":
				if !slices.Equal(gotProbes["/v1/messages"], []string{"deepseek-v3.2"}) {
					t.Fatalf("claude probes = %v", gotProbes)
				}
				cfg := readJSON(t, filepath.Join(s.fakeHome, claude.SettingsFile))
				env := cfg["env"].(map[string]any)
				if cfg["model"] != "deepseek-v3.2" || env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "glm-4.7-flash" {
					t.Fatalf("claude settings = %v", cfg)
				}
			case "codex":
				if !slices.Equal(gotProbes["/v1/responses"], []string{"deepseek-v3.2"}) {
					t.Fatalf("codex probes = %v", gotProbes)
				}
				if got := codexConfigOf(t, s).Model; got != "deepseek-v3.2" {
					t.Fatalf("codex model = %q, want deepseek-v3.2", got)
				}
			case "pi":
				if !slices.Contains(gotProbes["/v1/chat/completions"], "deepseek-v3.2") ||
					!strings.Contains(r.stderr, "pi --model aiven/deepseek-v3.2\n") {
					t.Fatalf("pi probes=%v stderr=%s", gotProbes, r.stderr)
				}
			case "opencode":
				if !slices.Equal(gotProbes["/v1/chat/completions"], []string{"deepseek-v3.2"}) ||
					!strings.Contains(r.stderr, "opencode -m aiven/deepseek-v3.2\n") {
					t.Fatalf("opencode probes=%v stderr=%s", gotProbes, r.stderr)
				}
			case "omp":
				if !slices.Contains(gotProbes["/v1/chat/completions"], "deepseek-v3.2") ||
					!strings.Contains(r.stderr, "omp --model aiven/deepseek-v3.2\n") {
					t.Fatalf("omp probes=%v stderr=%s", gotProbes, r.stderr)
				}
			}
		})
	}
}

func TestSetupZeroInstalledToolsExitsBeforeKeyAndNonInteractiveWithoutYesSavesNoKey(t *testing.T) {
	t.Parallel()

	t.Run("zero installed harnesses exits before obtainKey", func(t *testing.T) {
		t.Parallel()
		g := newGatewayDouble(t)
		s := newSession(t, g)
		s.runner.missing = []string{"pi", "opencode", "codex", "claude", "omp"}
		p := &scriptedPrompter{}
		s.deps.Prompter = p
		r := s.run("setup")
		if r.code != 0 || len(g.journal()) != 0 || len(p.prompts) != 0 ||
			!strings.Contains(r.stderr, "No supported coding harnesses found on PATH.") ||
			!strings.Contains(r.stderr, "aiven-ai-gateway login") {
			t.Fatalf("exit %d, requests %d, prompts %q, stderr:\n%s",
				r.code, len(g.journal()), p.prompts, r.stderr)
		}
		if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
			t.Error("key file created with zero installed tools")
		}
	})

	t.Run("non-interactive without --yes saves neither key nor model prefs", func(t *testing.T) {
		t.Parallel()
		g := newGatewayDouble(t)
		s := newSession(t, g).stdin(keyValid)
		// Prompter is nil (non-interactive) and --yes is omitted.
		r := s.run("setup", "pi", "--key-stdin")
		if r.code != 0 || !strings.Contains(r.stderr, "without a terminal, rerun with --yes to apply") {
			t.Fatalf("exit %d, stderr:\n%s", r.code, r.stderr)
		}
		if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
			t.Error("non-interactive setup without --yes saved key")
		}
		if _, err := os.Stat(config.ModelPrefsPath(s.fakeHome)); !os.IsNotExist(err) {
			t.Error("non-interactive setup without --yes saved model prefs")
		}
	})

	t.Run("cancelled setup saves neither key nor model prefs", func(t *testing.T) {
		t.Parallel()
		g := newGatewayDouble(t)
		s := newSession(t, g).stdin(keyProductionOwned)
		// Choose Open-weight (deepseek-v3.2), No direct (2), Decline (n).
		s.deps.Prompter = &scriptedPrompter{answers: []string{"4", "2", "n"}}
		r := s.run("setup", "pi", "--key-stdin", "--plain")
		if r.code != 0 || !strings.Contains(r.stderr, "declined") {
			t.Fatalf("exit %d, stderr:\n%s", r.code, r.stderr)
		}
		if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
			t.Error("declined setup saved key")
		}
		if _, err := os.Stat(config.ModelPrefsPath(s.fakeHome)); !os.IsNotExist(err) {
			t.Error("declined setup saved model prefs")
		}
	})
}

func TestSetupYesRefusesUngrantedModelAndSuggestsProviderAlias(t *testing.T) {
	t.Parallel()
	g := newGatewayDouble(t)
	s := newSession(t, g).stdin(keyProductionOwned)
	s.writeUserSettings("settings.json", `{"model":"eu.anthropic.claude-sonnet-5.5"}`)
	r := s.run("setup", "claude", "--key-stdin", "--yes")
	if r.code == 0 || !strings.Contains(r.stderr, "eu.anthropic.claude-sonnet-5.5") ||
		!strings.Contains(r.stderr, "try claude-sonnet-5-5") {
		t.Fatalf("exit %d, stderr:\n%s", r.code, r.stderr)
	}
	if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
		t.Error("key saved despite ungranted model")
	}
}

// R2: (a) User ~/.claude/settings.json with ANTHROPIC_MODEL=claude-retired + direct invocation OFF +
// selecting Open-weight succeeds, probes glm-5, writes blank ANTHROPIC_MODEL in aiven.settings.json, and
// launch claude runs glm-5; (b) project .claude/settings.local.json ungranted refuses without fake repair.
func TestClaudeUntouchedUserAndProjectLayerRecoveryBoundaries(t *testing.T) {
	t.Parallel()

	t.Run(
		"user ANTHROPIC_MODEL ungranted repaired in dedicated settings with direct OFF",
		func(t *testing.T) {
			t.Parallel()
			g := newGatewayDouble(t)
			probes := recordProbes(t, g)
			s := newSession(t, g).stdin(keyOpenWeightLA)
			s.writeUserSettings("settings.json", `{"env":{"ANTHROPIC_MODEL":"claude-retired"}}`)
			// Beat 2: 1=Keep unchanged, 2=Recommended defaults (Open-weight deepseek-v3.2); Beat 3: 2=No; Review: 1=Apply.
			s.deps.Prompter = &scriptedPrompter{answers: []string{"2", "2", "1"}}
			r := s.run("setup", "claude", "--key-stdin", "--plain")
			if r.code != 0 {
				t.Fatalf("setup exit %d, stderr:\n%s", r.code, r.stderr)
			}
			if got := probes()["/v1/messages"]; !slices.Equal(got, []string{"deepseek-v3.2"}) {
				t.Fatalf("probes = %v, want [deepseek-v3.2]", got)
			}
			cfg := readJSON(t, filepath.Join(s.fakeHome, claude.SettingsFile))
			env := cfg["env"].(map[string]any)
			if cfg["model"] != "deepseek-v3.2" || env["ANTHROPIC_MODEL"] != "" {
				t.Fatalf("aiven.settings.json = %v, want model=deepseek-v3.2 and blank ANTHROPIC_MODEL", cfg)
			}
			// Untouched ~/.claude/settings.json stays as user left it.
			userCfg := readJSON(t, filepath.Join(s.fakeHome, ".claude", "settings.json"))
			if userCfg["env"].(map[string]any)["ANTHROPIC_MODEL"] != "claude-retired" {
				t.Fatalf("user settings.json was unexpectedly modified: %v", userCfg)
			}
			// launch claude must run deepseek-v3.2, not claude-retired.
			if lr := s.run("launch", "claude"); lr.code != 0 {
				t.Fatalf("launch claude exit %d: %s", lr.code, lr.stderr)
			}
			if got, _ := s.launched().envVar("ANTHROPIC_MODEL"); got != "deepseek-v3.2" {
				t.Errorf("launch ANTHROPIC_MODEL = %q, want deepseek-v3.2", got)
			}
		},
	)

	t.Run("project settings.local.json ungranted refuses without fake repair", func(t *testing.T) {
		t.Parallel()
		g := newGatewayDouble(t)
		s := newSession(t, g).stdin(keyOpenWeightLA)
		s.writeProjectSettings("settings.local.json", `{"env":{"ANTHROPIC_MODEL":"claude-retired"}}`)
		p := &scriptedPrompter{answers: []string{"2", "1", "1"}}
		s.deps.Prompter = p
		r := s.run("setup", "claude", "--key-stdin", "--plain")
		projPath := filepath.Join(s.env.WorkDir, ".claude", "settings.local.json")
		if r.code == 0 || !strings.Contains(r.stderr, projPath) ||
			!strings.Contains(r.stderr, "does not grant") {
			t.Fatalf("exit %d, stderr:\n%s", r.code, r.stderr)
		}
		if len(p.prompts) != 0 {
			t.Errorf("offered Beat 2 repair prompts for unrepairable project override: %q", p.prompts)
		}
		if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
			t.Error("key saved despite unrepairable project override")
		}
	})
}

// R4: Repeat setup (--yes --plain) probes the persisted launcher model (deepseek-v3.2) rather than SonnetOrFirst.
func TestRepeatSetupProbesPersistedLauncherModel(t *testing.T) {
	t.Parallel()
	g := newGatewayDouble(t)
	s := newSession(t, g).stdin(keyProductionOwned)
	// First setup: choose Open-weight (option 4 -> deepseek-v3.2), direct OFF (2), Apply (1).
	s.deps.Prompter = &scriptedPrompter{answers: []string{"4", "2", "1"}}
	if r := s.run("setup", "opencode", "--key-stdin", "--plain"); r.code != 0 {
		t.Fatalf("first setup exit %d: %s", r.code, r.stderr)
	}
	// Second setup: --yes --plain with no prompt must probe deepseek-v3.2 (persisted), not claude-sonnet-5-5!
	probes := recordProbes(t, g)
	s.deps.Prompter = nil
	r := s.run("setup", "opencode", "--yes", "--plain")
	if r.code != 0 {
		t.Fatalf("repeat setup exit %d: %s", r.code, r.stderr)
	}
	if got := probes()["/v1/chat/completions"]; !slices.Equal(got, []string{"deepseek-v3.2"}) {
		t.Fatalf("repeat setup probed %v, want [deepseek-v3.2]", got)
	}
	if !strings.Contains(r.stderr, "opencode -m aiven/deepseek-v3.2") {
		t.Errorf("repeat setup output did not retain deepseek-v3.2:\n%s", r.stderr)
	}
}

func TestSetupPlainChoosesAGeminiModelForClaudeCode(t *testing.T) {
	t.Parallel()
	g := newGatewayDouble(t)
	probes := recordProbes(t, g)
	s := newSession(t, g).stdin(keyProductionOwned)
	// Beat 2: 2=Choose models, then gemini-4-argon by id; Beat 3: 2=No; Review: 1=Apply.
	s.deps.Prompter = &scriptedPrompter{answers: []string{"2", "gemini-4-argon", "2", "1"}}
	r := s.run("setup", "claude", "--key-stdin", "--plain")
	if r.code != 0 {
		t.Fatalf("setup exit %d: %s", r.code, r.stderr)
	}
	if got := probes()["/v1/messages"]; !slices.Equal(got, []string{"gemini-4-argon"}) {
		t.Fatalf("probes = %v, want [gemini-4-argon]", got)
	}
	cfg := readJSON(t, filepath.Join(s.fakeHome, claude.SettingsFile))
	env := cfg["env"].(map[string]any)
	if cfg["model"] != "gemini-4-argon[1m]" ||
		env["ANTHROPIC_CUSTOM_MODEL_OPTION"] != "gemini-4-argon[1m]" ||
		env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "claude-haiku-5.5" {
		t.Fatalf("claude settings = %v", cfg)
	}
}

func TestSetupGeminiPresetDefaultsToNeon(t *testing.T) {
	t.Parallel()
	s := newSession(t, newGatewayDouble(t)).stdin(keyGeminiFour)
	// Beat 2: 1=Gemini; Beat 3: 2=No; Review: y=Apply.
	s.deps.Prompter = &scriptedPrompter{answers: []string{"1", "2", "y"}}
	r := s.run("setup", "opencode", "--key-stdin", "--plain")
	if r.code != 0 || !strings.Contains(r.stderr, "opencode -m aiven/gemini-4-neon\n") {
		t.Fatalf("exit %d, stderr:\n%s", r.code, r.stderr)
	}
}
