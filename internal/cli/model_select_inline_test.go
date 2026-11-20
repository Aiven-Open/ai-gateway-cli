// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"context"
	"encoding/json"
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
	tableIdx := strings.Index(out, "TOOL")
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
