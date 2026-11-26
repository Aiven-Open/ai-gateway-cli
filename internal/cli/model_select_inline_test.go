// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

func inlineSetupEnv(t *testing.T, url string) (config.Env, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	work := filepath.Join(t.TempDir(), "work")
	for _, dir := range []string{home, work} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return config.Env{
		BaseURL:  url,
		UserHome: home,
		WorkDir:  work,
		Environ:  []string{"PATH=/usr/bin:/bin", config.HomeVar + "=" + home},
	}, home
}

func inlineGateway(t *testing.T, catalogue string) (*httptest.Server, func() map[string][]string) {
	t.Helper()
	var mu sync.Mutex
	probes := map[string][]string{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":` + catalogue + `}`))
			return
		}
		if r.Method == http.MethodPost {
			var body struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			probes[r.URL.Path] = append(probes[r.URL.Path], body.Model)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"id":"synthetic","choices":[],"content":[],"output":[]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv, func() map[string][]string {
		mu.Lock()
		defer mu.Unlock()
		out := make(map[string][]string, len(probes))
		for k, v := range probes {
			out[k] = append([]string(nil), v...)
		}
		return out
	}
}

type inlineSetupPrompter struct {
	scriptedSelector
	secret string
}

func (p *inlineSetupPrompter) ReadSecret(string) (string, error) {
	if p.secret == "" {
		return "", io.EOF
	}
	return p.secret, nil
}

// Pattern Y inline test:
// - Claude has an ungranted Bedrock ID in ~/.claude/settings.json (NOT aiven.settings.json!).
// - Beat 1 pre-selects all installed tools and badges Claude with `! ungranted: eu.anthropic.claude-sonnet-5.5` (never `new`).
// - Beat 2 resolves Claude's conflict inline without forcing [1/5]..[5/5] prompts across healthy tools!
// - Beat 3 toggles direct invocation ON for Claude (updating ~/.claude/settings.json) WITHOUT "setup write set changed during inspection"!
// - Zero raw ReadLine calls on TTY (sel.lines == 0).
// - Rail closes with └ before Review table & launch commands.
func TestInlinePatternYBadgesUserClaudeLayerResolvesInlineAndTogglesDirect(t *testing.T) {
	t.Parallel()
	srv, probes := inlineGateway(
		t,
		`[{"id":"claude-sonnet-5-5","owned_by":"Anthropic","context_window_tokens":1000000},{"id":"glm-5","owned_by":"Z.ai"},{"id":"gpt-6.1-sol","owned_by":"OpenAI"}]`,
	)
	env, home := inlineSetupEnv(t, srv.URL)
	userSettings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(userSettings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		userSettings,
		[]byte(`{"model":"eu.anthropic.claude-sonnet-5.5"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	p := &inlineSetupPrompter{
		secret: "sk-test-inline-valid",
		scriptedSelector: scriptedSelector{
			answers: []selectAnswer{
				// Beat 1 (Harnesses): accept pre-selected pi(0), claude(3)
				{picked: []int{0, 3}},
				// Beat 2 (Models inline conflict fix): 0 = Switch Claude Code -> claude-sonnet-5-5 & use Recommended defaults
				{picked: []int{0}},
				// Beat 3 (Direct invocation): 0 = Yes — all 2 tools directly (default, toggles asDefault ON!)
				{picked: []int{0}},
				// Review table: 0 = Apply all
				{picked: []int{0}},
			},
		},
	}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()

	code := Run(context.Background(), []string{"setup"}, env, deps)
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr.String())
	}
	if p.lines != 0 {
		t.Errorf("inline TTY invoked raw ReadLine %d times, want 0", p.lines)
	}
	// Verify Beat 1 pre-selected all tools and accurately badged Claude's ~/.claude/settings.json conflict.
	if len(p.asked) < 4 {
		t.Fatalf("asked %d menus, want 4", len(p.asked))
	}
	beat1 := p.asked[0]
	for _, opt := range beat1.options {
		if !opt.picked {
			t.Errorf("Beat 1 option %q not pre-selected", opt.label)
		}
		if opt.label == "Claude Code" &&
			!strings.Contains(opt.detail, "! ungranted: eu.anthropic.claude-sonnet-5.5") {
			t.Errorf("Beat 1 Claude badge = %q, want ungranted badge", opt.detail)
		}
	}
	// Verify Beat 2 resolved conflict in ONE menu (no [1/2]..[2/2] penalty).
	beat2 := p.asked[1]
	if !strings.Contains(beat2.options[0].label, "Switch Claude Code -> claude-sonnet-5-5") {
		t.Errorf("Beat 2 option 0 = %q", beat2.options[0].label)
	}
	// Verify ~/.claude/settings.json was cleanly updated without "setup write set changed during inspection".
	data, err := os.ReadFile(userSettings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"claude-sonnet-5-5[1m]"`) {
		t.Errorf("~/.claude/settings.json not updated: %s", data)
	}
	// Verify rail opened and closed BEFORE Review table and Start a session.
	out := stderr.String()
	if !strings.Contains(out, "┌") || !strings.Contains(out, "└") {
		t.Errorf("missing rail start/end glyphs:\n%s", out)
	}
	endIdx := strings.Index(out, "└")
	tableIdx := strings.Index(out, "Default provider  ")
	launchIdx := strings.Index(out, "aiven-ai-gateway launch claude")
	if endIdx < 0 || tableIdx < endIdx || launchIdx < endIdx {
		t.Errorf("rail did not close before Review table & launch commands:\n%s", out)
	}
	// Verify probes hit claude-sonnet-5-5.
	gotProbes := probes()
	if !slices.Contains(gotProbes["/v1/messages"], "claude-sonnet-5-5") {
		t.Errorf("probes = %v", gotProbes)
	}
}

// Verify Back navigation (Review -> Beat 3 -> Beat 2 -> Beat 1 -> Forward) sends ZERO duplicate probes
// for already-verified (protocol, model) pairs and writes nothing until final Apply.
func TestInlineBackNavigationMemoizesProbesAndUsesInlineEnvKeyMenu(t *testing.T) {
	t.Parallel()
	srv, probes := inlineGateway(
		t,
		`[{"id":"claude-sonnet-5-5","owned_by":"Anthropic"},{"id":"glm-5","owned_by":"Z.ai"}]`,
	)
	env, home := inlineSetupEnv(t, srv.URL)
	env.Environ = append(env.Environ, creds.EnvVar+"=sk-test-env-key-1234567890")

	p := &inlineSetupPrompter{
		scriptedSelector: scriptedSelector{
			answers: []selectAnswer{
				// Env-key inline menu (zero raw [y/N]!): 0 = Use AIVEN_AI_GATEWAY_KEY
				{picked: []int{0}},
				// Beat 1: select opencode (index 1)
				{picked: []int{1}},
				// Beat 2 (Models): 1 = Open-weight (glm-5)
				{picked: []int{1}},
				// Beat 3 (Direct): 1 = No
				{picked: []int{1}},
				// Review: errBack -> returns to Beat 3!
				{err: errBack},
				// Beat 3 again: 0 = Yes (direct opencode!)
				{picked: []int{0}},
				// Review again: 0 = Apply
				{picked: []int{0}},
			},
		},
	}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()

	code := Run(context.Background(), []string{"setup"}, env, deps)
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr.String())
	}
	if p.lines != 0 {
		t.Errorf("inline TTY used ReadLine %d times (expected inline env-key menu)", p.lines)
	}
	// Memoized probe: /v1/chat/completions with glm-5 must have been probed ONCE, not twice!
	gotProbes := probes()
	if got := gotProbes["/v1/chat/completions"]; len(got) != 1 || got[0] != "glm-5" {
		t.Errorf("chat completions probes = %v, want exactly [glm-5] (memoized across Back)", got)
	}
	// Both opencode.json default model AND ai-gateway-models.json should be written cleanly.
	pref, err := config.LoadModelPref(home, srv.URL, "opencode")
	if err != nil || pref != "glm-5" {
		t.Errorf("persisted opencode pref = %q, %v; want glm-5", pref, err)
	}
	ocData, err := os.ReadFile(filepath.Join(home, ".config", "opencode", "opencode.json"))
	if err != nil || !strings.Contains(string(ocData), `"aiven/glm-5"`) {
		t.Errorf("opencode.json after toggling direct ON = %s (%v)", ocData, err)
	}
}

// Verify project .claude/settings.local.json ungranted model is badged in Beat 1.
func TestInlineBeat1BadgesProjectLocalClaudeLayer(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, `[{"id":"glm-5","owned_by":"Z.ai"}]`)
	env, _ := inlineSetupEnv(t, srv.URL)
	projLocal := filepath.Join(env.WorkDir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(projLocal), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		projLocal,
		[]byte(`{"env":{"ANTHROPIC_MODEL":"claude-retired"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	p := &inlineSetupPrompter{
		secret: "sk-test-inline-valid",
		scriptedSelector: scriptedSelector{
			answers: []selectAnswer{
				{err: errCancelled},
			},
		},
	}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()

	code := Run(context.Background(), []string{"setup"}, env, deps)
	if code != 1 {
		t.Fatalf("exit %d, want 1 on cancel", code)
	}
	if len(p.asked) != 1 {
		t.Fatalf("asked %d menus, want 1", len(p.asked))
	}
	foundBadge := false
	for _, opt := range p.asked[0].options {
		if opt.label == "Claude Code" && strings.Contains(opt.detail, "! ungranted: claude-retired") {
			foundBadge = true
		}
	}
	if !foundBadge {
		t.Errorf(
			"Beat 1 did not badge project .claude/settings.local.json conflict: %+v",
			p.asked[0].options,
		)
	}
}

// Verify Customize per tool… opens a per-tool hub so the user can customize one harness (preserving
// Claude tier slots on custom picks) and select Done — continue without walking every tool, or enter
// Customize one by one… and step back to the hub.
func TestCustomizePerToolHubPicksOneToolThenDoneAndSupportsSequential(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, `[
		{"id":"claude-opus-5-5","owned_by":"Anthropic","context_length":1000000},
		{"id":"claude-sonnet-5-5","owned_by":"Anthropic","context_length":1000000},
		{"id":"claude-haiku-5-5","owned_by":"Anthropic","context_length":200000},
		{"id":"deepseek-v4.1-flash","owned_by":"DeepSeek","context_length":128000},
		{"id":"gpt-6.1-sol","owned_by":"OpenAI","context_length":1000000}
	]`)
	env, home := inlineSetupEnv(t, srv.URL)
	// Seed a stale ANTHROPIC_CUSTOM_MODEL_OPTION to verify custom pick overwrites it cleanly.
	aivenPath := filepath.Join(home, ".claude", "aiven.settings.json")
	if err := os.MkdirAll(filepath.Dir(aivenPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		aivenPath,
		[]byte(`{"env":{"ANTHROPIC_CUSTOM_MODEL_OPTION":"old-stale-model"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	p := &inlineSetupPrompter{
		secret: "sk-test-inline-valid",
		scriptedSelector: scriptedSelector{
			answers: []selectAnswer{
				// 0: Beat 1 (Harnesses): accept all 5 pre-selected (pi, opencode, codex, claude, omp)
				{picked: []int{0, 1, 2, 3, 4}},
				// 1: Beat 2 (Default model): 0=Recommended, 1=Anthropic, 2=OpenAI, 3=Open-weight, 4=Customize per tool…
				{picked: []int{4}},
				// 2: Customize hub: pick 5 = Customize one by one…
				{picked: []int{5}},
				// 3: Sequential [1/5] Pi: Esc back to Customize hub!
				{err: errBack},
				// 4: Customize hub: pick 3 = Claude Code directly!
				{picked: []int{3}},
				// 5: Claude Code menu: 0=Recommended defaults, 1=Choose models
				{picked: []int{1}},
				// 6: Choose models: Anthropic 0..2, DeepSeek 3, OpenAI 4; deepseek-v4.1-flash alone
				{picked: []int{3}},
				// 7: Customize hub (cursor now on 6 = Done — continue): press Enter on 6!
				{picked: []int{6}},
				// 8: Beat 3 (Default provider): 1 = No — only via launch
				{picked: []int{1}},
				// 9: Review: 0 = Apply all
				{picked: []int{0}},
			},
		},
	}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()

	code := Run(context.Background(), []string{"setup"}, env, deps)
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr.String())
	}
	if p.asked[1].title != "Default model" {
		t.Errorf("Beat 2 title = %q, want %q", p.asked[1].title, "Default model")
	}
	if !strings.Contains(p.asked[3].subtitle, "sets default for the launcher CLI") {
		t.Errorf("Pi subtitle = %q, want launcher CLI wording", p.asked[3].subtitle)
	}
	if p.asked[2].title != "Customize default model per tool" || p.asked[2].cursor != 0 {
		t.Errorf("initial hub = (%q, cursor=%d)", p.asked[2].title, p.asked[2].cursor)
	}
	if p.asked[7].cursor != 6 || p.asked[7].options[6].label != "Done — continue" {
		t.Errorf(
			"post-customize hub cursor = %d, opt[6] = %+v",
			p.asked[7].cursor,
			p.asked[7].options[6],
		)
	}
	if p.asked[7].options[3].detail != "deepseek-v4.1-flash" {
		t.Errorf("hub Claude detail = %q, want deepseek-v4.1-flash", p.asked[7].options[3].detail)
	}
	raw, err := os.ReadFile(aivenPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	envMap, _ := cfg["env"].(map[string]any)
	if cfg["model"] != "deepseek-v4.1-flash" ||
		envMap["ANTHROPIC_CUSTOM_MODEL_OPTION"] != "deepseek-v4.1-flash" ||
		envMap["ANTHROPIC_DEFAULT_SONNET_MODEL"] != "claude-sonnet-5-5" ||
		envMap["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "claude-haiku-5-5" ||
		envMap["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "claude-opus-5-5" {
		t.Fatalf("unexpected Claude settings after custom hub pick: %v", cfg)
	}
}

const multiModelTestCatalogue = `[
	{"id":"claude-sonnet-5-5","owned_by":"Anthropic"},
	{"id":"glm-5","owned_by":"Z.ai"},
	{"id":"gpt-6.1-sol","owned_by":"OpenAI"}
]`

func readJSONKey(t *testing.T, path, key string) any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	return settings[key]
}

// Running setup from $HOME (WorkDir == UserHome) must treat ~/.claude/settings.json as the
// repairable user layer rather than a read-only project file.
func TestSetupClaudeUserConflictRepairFromHomeDirectory(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, multiModelTestCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	env.WorkDir = home
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"model":"retired-model"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &inlineSetupPrompter{
		secret: "synthetic-key",
		scriptedSelector: scriptedSelector{answers: []selectAnswer{
			{picked: []int{1}}, // Recommended defaults, after Keep unchanged
			{picked: []int{0}}, // Write native default and repair user file
			{picked: []int{0}}, // Apply
		}},
	}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()
	if code := Run(t.Context(), []string{"setup", "claude"}, env, deps); code != 0 {
		t.Fatalf("home-directory setup exit=%d, stderr:\n%s", code, stderr.String())
	}
	if model := readJSONKey(t, path, "model"); model != "claude-sonnet-5-5" {
		t.Errorf("user settings model = %v, want claude-sonnet-5-5", model)
	}
}

// Explicit launcher-only Claude setup choice must override a granted ~/.claude/settings.json model
// in both protocol probes and subsequent `claude.Launch`.
func TestSetupClaudeLauncherUsesExplicitSetupChoice(t *testing.T) {
	t.Parallel()
	srv, probes := inlineGateway(t, multiModelTestCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	for _, setting := range []struct{ name, model string }{
		{"aiven.settings.json", "claude-sonnet-5-5"},
		{"settings.json", "glm-5"},
	} {
		path := filepath.Join(home, ".claude", setting.name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"model":"`+setting.model+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stderr strings.Builder
	p := &inlineSetupPrompter{
		secret: "synthetic-key",
		scriptedSelector: scriptedSelector{answers: []selectAnswer{
			{picked: []int{2}}, // Choose models, after Keep current and Recommended defaults
			{picked: []int{1}}, // gpt-6.1-sol: Anthropic 0, OpenAI 1, Z.ai 2
			{picked: []int{1}}, // Launch only
			{picked: []int{0}}, // Apply
		}},
	}
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()
	if code := Run(t.Context(), []string{"setup", "claude"}, env, deps); code != 0 {
		t.Fatalf("setup exit %d: %s", code, stderr.String())
	}
	if model := readJSONKey(
		t,
		filepath.Join(home, ".claude", "aiven.settings.json"),
		"model",
	); model != "gpt-6.1-sol" {
		t.Fatalf("dedicated model = %v, want gpt-6.1-sol", model)
	}
	launch, err := claude.Launch(claude.Input{
		BaseURL:  srv.URL,
		UserHome: home,
		WorkDir:  env.WorkDir,
		Table: []models.Model{
			{Alias: "claude-sonnet-5-5"},
			{Alias: "glm-5"},
			{Alias: "gpt-6.1-sol"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(launch.Env, "ANTHROPIC_MODEL=gpt-6.1-sol") ||
		!slices.Contains(probes()["/v1/messages"], "gpt-6.1-sol") {
		t.Errorf("launch env = %v, probes = %v; want gpt-6.1-sol", launch.Env, probes())
	}
}

// Unattended `--yes` setup must refuse an ungranted project .claude/settings.json model even when
// dedicated aiven.settings.json has a granted env.ANTHROPIC_MODEL that would otherwise mask it.
func TestUnattendedSetupRefusesProjectClaudeConflictMaskedByDedicatedEnv(t *testing.T) {
	t.Parallel()
	srv, probes := inlineGateway(t, multiModelTestCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	for path, body := range map[string]string{
		filepath.Join(home, ".claude", "aiven.settings.json"):  `{"env":{"ANTHROPIC_MODEL":"claude-sonnet-5-5"}}`,
		filepath.Join(env.WorkDir, ".claude", "settings.json"): `{"model":"retired-model"}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stderr strings.Builder
	deps := inlineDeps(&inlineSetupPrompter{secret: "synthetic-key"}, &stderr)
	deps.HTTP = srv.Client()
	code := Run(t.Context(), []string{"setup", "claude", "--yes"}, env, deps)
	_, keyErr := os.Stat(creds.Path(home, srv.URL))
	if code == 0 || !os.IsNotExist(keyErr) || len(probes()) != 0 {
		t.Errorf(
			"unattended setup exit=%d, keyErr=%v, probes=%v; want refusal with no key or probes",
			code,
			keyErr,
			probes(),
		)
	}
}

// Backing out of Customize one by one… must discard abandoned choices without leaking them into
// the hub draft or Apply, while backing out from Beat 3 must preserve committed hub choices.
func TestSequentialBackDiscardsAbandonedChoiceAndRetainsHubDraftAcrossBeat3Back(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, multiModelTestCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	p := &inlineSetupPrompter{
		secret: "synthetic-key",
		scriptedSelector: scriptedSelector{answers: []selectAnswer{
			{picked: []int{0, 1}}, // Beat 1: Pi and OpenCode
			{picked: []int{4}},    // Beat 2: Customize per tool…
			{picked: []int{2}},    // Hub: Customize one by one…
			{picked: []int{1}},    // Sequential Pi: OpenAI (will be abandoned)
			{err: errBack},        // Sequential OpenCode -> Pi
			{err: errBack},        // Sequential Pi -> Hub (Pi choice discarded)
			{picked: []int{1}},    // Hub: OpenCode directly
			{picked: []int{2}},    // OpenCode: Open-weight (glm-5)
			{picked: []int{3}},    // Hub: Done — continue
			{err: errBack},        // Beat 3 -> Beat 2
			{picked: []int{4}},    // Beat 2 -> Customize per tool… (OpenCode still Open-weight!)
			{picked: []int{3}},    // Hub: Done — continue
			{picked: []int{0}},    // Beat 3: Yes — all 2 tools
			{picked: []int{0}},    // Review: Apply all
		}},
	}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()
	if code := Run(t.Context(), []string{"setup"}, env, deps); code != 0 {
		t.Fatalf("setup exit %d: %s", code, stderr.String())
	}
	if p.asked[6].options[0].detail != "claude-sonnet-5-5 (default)" {
		t.Errorf("hub Pi detail after sequential Back = %q", p.asked[6].options[0].detail)
	}
	if p.asked[11].options[1].detail != "Open-weight" {
		t.Errorf("reopened hub OpenCode detail after Beat 3 Back = %q", p.asked[11].options[1].detail)
	}
	if got := readJSONKey(
		t,
		filepath.Join(home, ".pi", "agent", "settings.json"),
		"defaultModel",
	); got != "claude-sonnet-5-5" {
		t.Errorf("Pi native default = %v, want claude-sonnet-5-5", got)
	}
	if got := readJSONKey(
		t,
		filepath.Join(home, ".config", "opencode", "opencode.json"),
		"model",
	); got != "aiven/glm-5" {
		t.Errorf("OpenCode native default = %v, want aiven/glm-5", got)
	}
}

// Repeat setup must persist an explicit launcher preference even when it equals today's fallback.
func TestRepeatSetupPersistsExplicitCurrentDefaultPreference(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, multiModelTestCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	var stderr strings.Builder
	seed := inlineDeps(&inlineSetupPrompter{secret: "synthetic-key"}, &stderr)
	seed.HTTP = srv.Client()
	if code := Run(t.Context(), []string{"setup", "opencode", "--yes"}, env, seed); code != 0 {
		t.Fatalf("initial unattended setup exit %d: %s", code, stderr.String())
	}
	stderr.Reset()
	p := &inlineSetupPrompter{
		secret: "synthetic-key",
		scriptedSelector: scriptedSelector{answers: []selectAnswer{
			{picked: []int{0}}, // Anthropic preset explicitly chosen
			{picked: []int{1}}, // Launch only
			{picked: []int{0}}, // Apply
		}},
	}
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()
	if code := Run(t.Context(), []string{"setup", "opencode"}, env, deps); code != 0 {
		t.Fatalf("setup exit %d: %s", code, stderr.String())
	}
	alias, err := config.LoadModelPref(home, srv.URL, "opencode")
	if err != nil || alias != "claude-sonnet-5-5" {
		t.Errorf("launcher preference = %q, %v; want claude-sonnet-5-5", alias, err)
	}
}

// Retaining defaults when a valid launcher preference exists must write that same model to the
// native default config and probe it consistently.
func TestSetupAlignsNativeDefaultAndProbeWithPersistedLauncherPreference(t *testing.T) {
	t.Parallel()
	srv, probes := inlineGateway(t, multiModelTestCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	if err := config.SaveModelPrefs(
		home,
		srv.URL,
		map[string]string{"opencode": "glm-5"},
		nil,
	); err != nil {
		t.Fatal(err)
	}
	p := &inlineSetupPrompter{
		secret: "synthetic-key",
		scriptedSelector: scriptedSelector{answers: []selectAnswer{
			{picked: []int{0, 1}}, // Pi and OpenCode
			{picked: []int{0}},    // Recommended defaults
			{picked: []int{0}},    // Yes — all 2 tools
			{picked: []int{0}},    // Apply all
		}},
	}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()
	if code := Run(t.Context(), []string{"setup"}, env, deps); code != 0 {
		t.Fatalf("setup exit %d: %s", code, stderr.String())
	}
	if model := readJSONKey(
		t,
		filepath.Join(home, ".config", "opencode", "opencode.json"),
		"model",
	); model != "aiven/glm-5" {
		t.Errorf("OpenCode native default = %v, want aiven/glm-5", model)
	}
	if !slices.Contains(probes()["/v1/chat/completions"], "glm-5") {
		t.Errorf("probes = %v, want glm-5 over Chat Completions", probes())
	}
}

// Choosing models takes the tiers from the catalogue, never from a pin the settings already hold.
func TestSetupClaudeChoosingModelsTakesTiersFromCatalogue(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, `[
		{"id":"claude-sonnet-4-5","owned_by":"Anthropic"},
		{"id":"claude-sonnet-5-5","owned_by":"Anthropic"},
		{"id":"glm-5","owned_by":"Z.ai"}
	]`)
	env, home := inlineSetupEnv(t, srv.URL)
	path := filepath.Join(home, ".claude", "aiven.settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		path,
		[]byte(`{"env":{"ANTHROPIC_DEFAULT_SONNET_MODEL":"claude-sonnet-4-5"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	p := &inlineSetupPrompter{
		secret: "synthetic-key",
		scriptedSelector: scriptedSelector{answers: []selectAnswer{
			{picked: []int{1}}, // Choose models, after Recommended defaults
			{picked: []int{2}}, // glm-5: Anthropic 0..1, Z.ai 2
			{picked: []int{1}}, // Launch only
			{picked: []int{0}}, // Apply
		}},
	}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()
	if code := Run(t.Context(), []string{"setup", "claude"}, env, deps); code != 0 {
		t.Fatalf("setup exit %d: %s", code, stderr.String())
	}
	wired := readJSONKey(t, path, "env").(map[string]any)
	if got := wired["ANTHROPIC_DEFAULT_SONNET_MODEL"]; got != "claude-sonnet-5-5" {
		t.Errorf("Sonnet tier after choosing models = %q, want the catalogue's claude-sonnet-5-5", got)
	}
}

// Malformed ai-gateway-models.json must fail before probes or approval without saving a key or files.
func TestSetupMalformedModelPrefsRefusedBeforeApproval(t *testing.T) {
	t.Parallel()
	srv, probes := inlineGateway(t, multiModelTestCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	prefsPath := config.ModelPrefsPath(home)
	if err := os.MkdirAll(filepath.Dir(prefsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prefsPath, []byte("{bad-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &inlineSetupPrompter{
		secret: "synthetic-key",
		scriptedSelector: scriptedSelector{answers: []selectAnswer{
			{picked: []int{2}}, // Open-weight (glm-5)
			{picked: []int{1}}, // Launch only
		}},
	}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()
	code := Run(t.Context(), []string{"setup", "opencode"}, env, deps)
	if code == 0 || !strings.Contains(stderr.String(), "is not a valid model preference file") {
		t.Fatalf("exit=%d, stderr:\n%s", code, stderr.String())
	}
	if len(probes()) != 0 {
		t.Errorf("sent probes despite malformed prefs: %v", probes())
	}
	if _, err := os.Stat(creds.Path(home, srv.URL)); !os.IsNotExist(err) {
		t.Errorf("key saved despite malformed prefs: %v", err)
	}
}

// A post-approval SaveModelPrefs failure must still emit the finish summary naming completed files and backups.
func TestSetupModelPrefsWriteFailureReportsCompletedWrites(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, multiModelTestCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	ocPath := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(ocPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ocPath, []byte(`{"existing":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &inlineSetupPrompter{
		secret: "synthetic-key",
		scriptedSelector: scriptedSelector{answers: []selectAnswer{
			{picked: []int{2}}, // Open-weight (glm-5)
			{picked: []int{1}}, // Launch only
			{picked: []int{0}}, // Apply
		}},
	}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()
	prefsPath := config.ModelPrefsPath(home)
	deps.WriteFile = func(path string, data []byte, perm os.FileMode) error {
		if path == prefsPath {
			return errors.New("simulated prefs disk error")
		}
		return fsutil.WriteAtomic(path, data, perm)
	}
	code := Run(t.Context(), []string{"setup", "opencode"}, env, deps)
	out := stderr.String()
	if code == 0 ||
		!strings.Contains(out, "Setup finished with errors") ||
		!strings.Contains(out, "partially configured") ||
		!strings.Contains(out, "written: "+ocPath) ||
		!strings.Contains(out, ".bak") {
		t.Fatalf("exit=%d, stderr:\n%s", code, out)
	}
}

// Plain-mode per-tool direct-provider menu must reject tool names outside the offered subset,
// and plain-mode customize hub Enter default must match the inline cursor (Done — continue after edit).
func TestPlainCustomizeHubDefaultCursorAndDirectSubsetValidation(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, multiModelTestCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	prompts := &plainSetupPrompter{
		secret: "synthetic-key",
		answers: []string{
			"pi,opencode", // Beat 1: select pi and opencode only
			"5",           // Beat 2: Customize per tool…
			"1",           // Hub: Pi
			"2",           // Pi: OpenAI (gpt-6.1-sol)
			"",            // Hub: Enter now defaults to 4 (Done — continue), matching inline cursor!
			"3",           // Beat 3: Choose per tool…
			"claude",      // Unoffered tool name -> must be rejected and re-prompted!
			"pi",          // Offered tool name -> accepted
			"1",           // Review: Apply all
		},
	}
	var stderr strings.Builder
	deps := Deps{
		Stderr:   &stderr,
		Stdout:   io.Discard,
		HTTP:     srv.Client(),
		Prompter: prompts,
		Runner:   allInstalled{},
	}
	code := Run(t.Context(), []string{"setup", "--plain"}, env, deps)
	out := stderr.String()
	if code != 0 {
		t.Fatalf("exit=%d, stderr:\n%s", code, out)
	}
	if !strings.Contains(out, "Choose from the list, for example 1,2 or pi,codex.") {
		t.Errorf("unoffered tool name was not rejected:\n%s", out)
	}
	if !slices.Contains(prompts.asked, `Choose model option (Enter for 4, or "back"): `) {
		t.Errorf("hub plain prompt did not default Enter to Done (4): %q", prompts.asked)
	}
	if got := readJSONKey(
		t,
		filepath.Join(home, ".pi", "agent", "settings.json"),
		"defaultModel",
	); got != "gpt-6.1-sol" {
		t.Errorf("Pi native default = %v, want gpt-6.1-sol", got)
	}
}

func TestUnattendedSetupPreservesAndValidatesCanonicalUserEnvModel(t *testing.T) {
	t.Parallel()
	const cat = `[
		{"id":"claude-sonnet-5-5","owned_by":"Anthropic"},
		{"id":"glm-5","owned_by":"Z.ai"}
	]`
	t.Run("keeps granted user ANTHROPIC_MODEL over dedicated ANTHROPIC_MODEL", func(t *testing.T) {
		t.Parallel()
		srv, probes := inlineGateway(t, cat)
		env, home := inlineSetupEnv(t, srv.URL)
		claudeDir := filepath.Join(home, ".claude")
		if err := os.MkdirAll(claudeDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(claudeDir, "aiven.settings.json"),
			[]byte(`{"env":{"ANTHROPIC_MODEL":"claude-sonnet-5-5"}}`),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(claudeDir, "settings.json"),
			[]byte(`{"env":{"ANTHROPIC_MODEL":"glm-5"}}`),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		var stderr strings.Builder
		deps := inlineDeps(nil, &stderr)
		deps.Stdin = strings.NewReader("sk-test-inline-valid\n")
		deps.HTTP = srv.Client()
		code := Run(
			context.Background(),
			[]string{"setup", "claude", "--yes", "--key-stdin"},
			env,
			deps,
		)
		if code != 0 {
			t.Fatalf("exit %d, stderr:\n%s", code, stderr.String())
		}
		// The kept dedicated model is checked too, since it runs outside the user's own selection.
		got := slices.Sorted(slices.Values(probes()["/v1/messages"]))
		if !slices.Equal(got, []string{"claude-sonnet-5-5", "glm-5"}) {
			t.Errorf("Messages probes = %v, want [claude-sonnet-5-5 glm-5]", got)
		}
	})
	t.Run("refuses ungranted user ANTHROPIC_MODEL over dedicated ANTHROPIC_MODEL", func(t *testing.T) {
		t.Parallel()
		srv, probes := inlineGateway(t, cat)
		env, home := inlineSetupEnv(t, srv.URL)
		claudeDir := filepath.Join(home, ".claude")
		if err := os.MkdirAll(claudeDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(claudeDir, "aiven.settings.json"),
			[]byte(`{"env":{"ANTHROPIC_MODEL":"claude-sonnet-5-5"}}`),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(claudeDir, "settings.json"),
			[]byte(`{"env":{"ANTHROPIC_MODEL":"retired-model"}}`),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		var stderr strings.Builder
		deps := inlineDeps(nil, &stderr)
		deps.Stdin = strings.NewReader("sk-test-inline-valid\n")
		deps.HTTP = srv.Client()
		code := Run(
			context.Background(),
			[]string{"setup", "claude", "--yes", "--key-stdin"},
			env,
			deps,
		)
		if code != 1 {
			t.Fatalf("exit %d, want 1; stderr:\n%s", code, stderr.String())
		}
		if got := probes()["/v1/messages"]; len(got) != 0 {
			t.Errorf("Messages probes = %v, want none", got)
		}
		if _, err := os.Stat(creds.Path(home, srv.URL)); !os.IsNotExist(err) {
			t.Errorf("key file should not be created on ungranted user refusal, stat err = %v", err)
		}
	})
}

func TestLauncherOnlyChoosingModelsTakesTiersFromCatalogue(t *testing.T) {
	t.Parallel()
	srv, probes := inlineGateway(t, `[
		{"id":"claude-opus-5-5","owned_by":"Anthropic"},
		{"id":"claude-sonnet-5-5","owned_by":"Anthropic"},
		{"id":"claude-sonnet-4-5","owned_by":"Anthropic"},
		{"id":"claude-haiku-5-5","owned_by":"Anthropic"},
		{"id":"glm-5","owned_by":"Z.ai"}
	]`)
	env, home := inlineSetupEnv(t, srv.URL)
	userSettings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(userSettings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		userSettings,
		[]byte(
			`{"model":"claude-sonnet-5-5","env":{"ANTHROPIC_DEFAULT_SONNET_MODEL":"claude-sonnet-4-5"}}`,
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	p := &inlineSetupPrompter{
		secret: "sk-test-inline-valid",
		scriptedSelector: scriptedSelector{
			answers: []selectAnswer{
				// Claude menu: 0=Keep current, 1=Recommended defaults, 2=Choose models
				{picked: []int{2}},
				// Choose models: Anthropic 0..3, Z.ai 4 = glm-5
				{picked: []int{4}},
				// Beat 3: 1 = No — only via launch
				{picked: []int{1}},
				// Review: 0 = Apply
				{picked: []int{0}},
			},
		},
	}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()

	code := Run(context.Background(), []string{"setup", "claude"}, env, deps)
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr.String())
	}
	if got := optionLabels(p.asked[0]); !slices.Equal(
		got, []string{"Keep current", "Recommended defaults", "Choose models"}) {
		t.Errorf("menu = %q", got)
	}
	if got := probes()["/v1/messages"]; !slices.Equal(got, []string{"glm-5"}) {
		t.Errorf("Messages probes = %v, want [glm-5]", got)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "aiven.settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	envMap, _ := cfg["env"].(map[string]any)
	if cfg["model"] != "glm-5" ||
		envMap["ANTHROPIC_CUSTOM_MODEL_OPTION"] != "glm-5" ||
		envMap["ANTHROPIC_DEFAULT_SONNET_MODEL"] != "claude-sonnet-5-5" {
		t.Fatalf("expected the catalogue's Sonnet tier with glm-5 custom pick, got %v", cfg)
	}
}

func TestBeat2RecommendedDefaultsShowsPersistedLauncherPreference(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, `[
		{"id":"claude-sonnet-5-5","owned_by":"Anthropic"},
		{"id":"glm-5","owned_by":"Z.ai"},
		{"id":"gpt-6.1-sol","owned_by":"OpenAI"}
	]`)
	env, home := inlineSetupEnv(t, srv.URL)
	if err := config.SaveModelPrefs(
		home,
		srv.URL,
		map[string]string{"opencode": "glm-5"},
		nil,
	); err != nil {
		t.Fatal(err)
	}
	p := &inlineSetupPrompter{
		secret: "sk-test-inline-valid",
		scriptedSelector: scriptedSelector{
			answers: []selectAnswer{
				// Beat 1: pi(0), opencode(1)
				{picked: []int{0, 1}},
				// Beat 2: cancel after inspecting Recommended defaults detail
				{err: errCancelled},
			},
		},
	}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()

	code := Run(context.Background(), []string{"setup"}, env, deps)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if len(p.asked) < 2 || len(p.asked[1].options) == 0 {
		t.Fatalf("unexpected Beat 2 menus: %+v", p.asked)
	}
	detail := p.asked[1].options[0].detail
	if !strings.Contains(detail, "OpenCode: glm-5") {
		t.Errorf("Beat 2 Recommended defaults detail = %q, want OpenCode: glm-5", detail)
	}
}

func TestBeat2RecommendedDefaultsShowsClaudePresetAliasWhenDistinctFromPi(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, `[
		{"id":"claude-sonnet","owned_by":"Anthropic"},
		{"id":"claude-sonnet-4-5","owned_by":"Anthropic"}
	]`)
	env, _ := inlineSetupEnv(t, srv.URL)
	p := &inlineSetupPrompter{
		secret: "sk-test-inline-valid",
		scriptedSelector: scriptedSelector{
			answers: []selectAnswer{
				// Beat 1: pi(0), claude(3)
				{picked: []int{0, 3}},
				// Beat 2: cancel after inspecting Recommended defaults detail
				{err: errCancelled},
			},
		},
	}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()

	if code := Run(context.Background(), []string{"setup"}, env, deps); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	detail := p.asked[1].options[0].detail
	if !strings.Contains(detail, "Claude Code: claude-sonnet-4-5") {
		t.Errorf("Beat 2 Recommended defaults detail = %q, want Claude Code: claude-sonnet-4-5", detail)
	}
}

func TestLauncherOnlyClaudeChoicesOverrideUserTierAndCustomPinsOnLaunch(t *testing.T) {
	t.Parallel()
	const cat = `[
		{"id":"claude-sonnet-5-5","owned_by":"Anthropic"},
		{"id":"claude-sonnet-4-5","owned_by":"Anthropic"},
		{"id":"gpt-6.1-sol","owned_by":"OpenAI"},
		{"id":"glm-5","owned_by":"Z.ai"}
	]`
	userJSON := `{"model":"claude-sonnet-5-5","env":{"ANTHROPIC_DEFAULT_SONNET_MODEL":"claude-sonnet-4-5","ANTHROPIC_CUSTOM_MODEL_OPTION":"gpt-6.1-sol"}}`

	t.Run("recommended defaults own the tier slots on launch", func(t *testing.T) {
		t.Parallel()
		srv, _ := inlineGateway(t, cat)
		env, home := inlineSetupEnv(t, srv.URL)
		userSettings := filepath.Join(home, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(userSettings), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(userSettings, []byte(userJSON), 0o600); err != nil {
			t.Fatal(err)
		}
		p := &inlineSetupPrompter{
			secret: "sk-test-inline-valid",
			scriptedSelector: scriptedSelector{
				answers: []selectAnswer{
					// Claude menu: 0=Keep current, 1=Recommended defaults
					{picked: []int{1}},
					// Beat 3: 1 = No — only via launch
					{picked: []int{1}},
					// Review: 0 = Apply
					{picked: []int{0}},
				},
			},
		}
		var stderr strings.Builder
		deps := inlineDeps(p, &stderr)
		deps.HTTP = srv.Client()
		if code := Run(context.Background(), []string{"setup", "claude"}, env, deps); code != 0 {
			t.Fatalf("setup exit %d, stderr:\n%s", code, stderr.String())
		}
		runner := &inlineRunner{}
		launchDeps := Deps{Stderr: &stderr, HTTP: srv.Client(), Runner: runner}
		if code := Run(context.Background(), []string{"launch", "claude"}, env, launchDeps); code != 0 {
			t.Fatalf("launch exit %d, stderr:\n%s", code, stderr.String())
		}
		if !slices.Contains(runner.lastEnv, "ANTHROPIC_DEFAULT_SONNET_MODEL=claude-sonnet-5-5") ||
			!slices.Contains(runner.lastEnv, "ANTHROPIC_CUSTOM_MODEL_OPTION=") {
			t.Fatalf("expected Anthropic preset tier and blank custom option, got env %v", runner.lastEnv)
		}
	})

	t.Run(
		"choosing a model owns the custom option and tiers come from the catalogue",
		func(t *testing.T) {
			t.Parallel()
			srv, _ := inlineGateway(t, cat)
			env, home := inlineSetupEnv(t, srv.URL)
			userSettings := filepath.Join(home, ".claude", "settings.json")
			if err := os.MkdirAll(filepath.Dir(userSettings), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(userSettings, []byte(userJSON), 0o600); err != nil {
				t.Fatal(err)
			}
			p := &inlineSetupPrompter{
				secret: "sk-test-inline-valid",
				scriptedSelector: scriptedSelector{
					answers: []selectAnswer{
						// Claude menu: 0=Keep current, 1=Recommended defaults, 2=Choose models
						{picked: []int{2}},
						// Choose models: Anthropic 0..1, OpenAI 2, Z.ai 3 = glm-5
						{picked: []int{3}},
						// Beat 3: 1 = No — only via launch
						{picked: []int{1}},
						// Review: 0 = Apply
						{picked: []int{0}},
					},
				},
			}
			var stderr strings.Builder
			deps := inlineDeps(p, &stderr)
			deps.HTTP = srv.Client()
			if code := Run(context.Background(), []string{"setup", "claude"}, env, deps); code != 0 {
				t.Fatalf("setup exit %d, stderr:\n%s", code, stderr.String())
			}
			runner := &inlineRunner{}
			launchDeps := Deps{Stderr: &stderr, HTTP: srv.Client(), Runner: runner}
			if code := Run(context.Background(), []string{"launch", "claude"}, env, launchDeps); code != 0 {
				t.Fatalf("launch exit %d, stderr:\n%s", code, stderr.String())
			}
			if !slices.Contains(runner.lastEnv, "ANTHROPIC_DEFAULT_SONNET_MODEL=claude-sonnet-5-5") ||
				!slices.Contains(runner.lastEnv, "ANTHROPIC_CUSTOM_MODEL_OPTION=glm-5") {
				t.Fatalf(
					"expected custom glm-5 and the catalogue's claude-sonnet-5-5 tier, got env %v",
					runner.lastEnv,
				)
			}
		},
	)
}

type inlineRunner struct {
	lastEnv []string
}

func (*inlineRunner) LookPath(name string) (string, error) { return "/usr/bin/" + name, nil }
func (r *inlineRunner) Exec(_ string, _ []string, env []string) error {
	r.lastEnv = append([]string(nil), env...)
	return nil
}

type plainSetupPrompter struct {
	secret  string
	answers []string
	asked   []string
	idx     int
}

func (p *plainSetupPrompter) ReadSecret(string) (string, error) {
	return p.secret, nil
}

func (p *plainSetupPrompter) ReadLine(prompt string) (string, error) {
	p.asked = append(p.asked, prompt)
	if p.idx >= len(p.answers) {
		return "", io.EOF
	}
	a := p.answers[p.idx]
	p.idx++
	return a, nil
}

// A change declined at review saves nothing, even when another selected tool is already up to date.
func TestSetupDeclinedChangeLeavesSavedKeyAlone(t *testing.T) {
	srv, _ := inlineGateway(t, multiModelTestCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	run := func(secret string, answers ...selectAnswer) string {
		p := &inlineSetupPrompter{secret: secret, scriptedSelector: scriptedSelector{answers: answers}}
		var stderr strings.Builder
		deps := inlineDeps(p, &stderr)
		deps.HTTP = srv.Client()
		deps.Stdin = strings.NewReader(secret + "\n")
		if code := Run(t.Context(), []string{"setup", "--key-stdin"}, env, deps); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr.String())
		}
		return stderr.String()
	}
	// Pi and OpenCode, recommended defaults, Yes for all, then Apply.
	run("synthetic-key", pick(0, 1), pick(0), pick(0), pick(0))
	if err := os.Remove(filepath.Join(home, ".config", "opencode", "opencode.json")); err != nil {
		t.Fatal(err)
	}
	// Pi is now up to date; OpenCode needs a change, and Cancel declines it.
	out := run("rotated-key", pick(0, 1), pick(0), pick(0), pick(3))
	got, err := os.ReadFile(creds.Path(home, srv.URL))
	if err != nil || string(got) != "synthetic-key\n" {
		t.Errorf("key file = %q, %v; want the key from before the run", got, err)
	}
	if !strings.Contains(out, "not saved by this run; existing key files unchanged") ||
		strings.Contains(out, "Saving the key") {
		t.Errorf("summary does not say the key was not saved:\n%s", out)
	}
}

func pick(picked ...int) selectAnswer { return selectAnswer{picked: picked} }
