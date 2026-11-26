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
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

// refusingGateway serves catalogue and answers the basic check for refused with HTTP 400.
func refusingGateway(t *testing.T, catalogue, refused string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"object":"list","data":` + catalogue + `}`))
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model == refused {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"model not served"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"synthetic","content":[],"choices":[],"output":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSetupBlocksApplyWhenChosenModelFailsItsCheck(t *testing.T) {
	t.Parallel()
	srv := refusingGateway(t, claudeMixCatalogue, "gpt-6-astra")
	env, home := inlineSetupEnv(t, srv.URL)
	p := &inlineSetupPrompter{secret: "synthetic-key", scriptedSelector: scriptedSelector{
		answers: []selectAnswer{
			{picked: []int{1}},    // Choose models
			{picked: []int{4, 5}}, // gemini-4-neon, gpt-6-astra
			{picked: []int{0}},    // default gemini-4-neon
			{picked: []int{1}},    // launch only
			{picked: []int{0}},    // Apply, which must not be offered
		},
	}}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()
	if code := Run(t.Context(), []string{"setup", "claude"}, env, deps); code != 1 {
		t.Fatalf("setup exit %d: %s", code, stderr.String())
	}
	if out := stderr.String(); !strings.Contains(out, "gpt-6-astra") ||
		!strings.Contains(out, "model not served") {
		t.Errorf("failure does not name the model:\n%s", out)
	}
	if len(p.answers) != 1 {
		t.Errorf("Apply was offered: %d answers left, want 1", len(p.answers))
	}
	if _, err := os.Stat(filepath.Join(home, claude.SettingsFile)); !os.IsNotExist(err) {
		t.Errorf("settings written after a failed check: %v", err)
	}
}

func TestSetupFailsOnlyClaudeCodeWhenItsPickIsRefused(t *testing.T) {
	t.Parallel()
	srv := refusingGateway(t, claudeMixCatalogue, "gpt-6-astra")
	env, home := inlineSetupEnv(t, srv.URL)
	p := &inlineSetupPrompter{secret: "synthetic-key", scriptedSelector: scriptedSelector{
		answers: []selectAnswer{
			{picked: []int{1, 3}}, // Tools: opencode and Claude Code
			{picked: []int{5}},    // Customize per tool
			{picked: []int{1}},    // hub: Claude Code
			{picked: []int{1}},    // Claude Code: Choose models
			{picked: []int{4, 5}}, // gemini-4-neon, gpt-6-astra
			{picked: []int{0}},    // default gemini-4-neon
			{picked: []int{3}},    // hub: Done
			{picked: []int{1}},    // launch only
			{picked: []int{0}},    // Review: Apply, still offered for OpenCode
		},
	}}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()
	code := Run(t.Context(), []string{"setup"}, env, deps)
	out := stderr.String()
	if code != 1 || len(p.answers) != 0 {
		t.Fatalf("exit %d, %d answers left, want 1 and 0:\n%s", code, len(p.answers), out)
	}
	if !strings.Contains(out, "gpt-6-astra") || !strings.Contains(out, "model not served") {
		t.Errorf("failure does not name the model:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.json")); err != nil {
		t.Errorf("OpenCode, whose check passed, was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, claude.SettingsFile)); !os.IsNotExist(err) {
		t.Errorf("Claude Code settings written after a failed check: %v", err)
	}
}

// claudeMixCatalogue grants three Claude tiers and three models from other vendors. The model list shows them as
// Anthropic 0 haiku, 1 opus, 2 sonnet; DeepSeek 3; Google 4; OpenAI 5.
const claudeMixCatalogue = `[
	{"id":"claude-opus-5-5","owned_by":"Anthropic"},
	{"id":"claude-sonnet-5-5","owned_by":"Anthropic"},
	{"id":"claude-haiku-5-5","owned_by":"Anthropic"},
	{"id":"deepseek-v4.1-flash","owned_by":"DeepSeek"},
	{"id":"gemini-4-neon","owned_by":"Google"},
	{"id":"gpt-6-astra","owned_by":"OpenAI"}
]`

// claudeWideCatalogue grants one Claude model and six from other vendors, listed as Alibaba 0 (1 in plain mode),
// Anthropic 1, DeepSeek 2, Google 3, Moonshot AI 4, OpenAI 5, Z.ai 6.
const claudeWideCatalogue = `[
	{"id":"claude-sonnet-5-5","owned_by":"Anthropic"},
	{"id":"deepseek-v4.1-flash","owned_by":"DeepSeek"},
	{"id":"gemini-4-neon","owned_by":"Google"},
	{"id":"glm-5","owned_by":"Z.ai"},
	{"id":"gpt-6-astra","owned_by":"OpenAI"},
	{"id":"kimi-k3","owned_by":"Moonshot AI"},
	{"id":"qwen3-coder-480b","owned_by":"Alibaba"}
]`

type claudeRun struct {
	asked  []choice
	env    config.Env
	home   string
	srv    *httptest.Server
	probes func() map[string][]string
}

// chooseThreeClaudeModels runs setup claude through Choose models, ticking Gemini, GPT and DeepSeek in that order,
// with Gemini as the default and launch-only wiring.
func chooseThreeClaudeModels(t *testing.T) claudeRun {
	t.Helper()
	srv, probes := inlineGateway(t, claudeMixCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	p := &inlineSetupPrompter{secret: "synthetic-key", scriptedSelector: scriptedSelector{
		answers: []selectAnswer{
			{picked: []int{1}},       // Models for Claude Code: 0 Recommended defaults, 1 Choose models
			{picked: []int{4, 5, 3}}, // gemini-4-neon, gpt-6-astra, deepseek-v4.1-flash, in that order
			{picked: []int{0}},       // Default model for Claude Code: gemini-4-neon
			{picked: []int{1}},       // Default provider: launch only
			{picked: []int{0}},       // Review: Apply
		},
	}}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()
	if code := Run(t.Context(), []string{"setup", "claude"}, env, deps); code != 0 {
		t.Fatalf("setup exit %d: %s", code, stderr.String())
	}
	return claudeRun{asked: p.asked, env: env, home: home, srv: srv, probes: probes}
}

func optionLabels(c choice) []string {
	var out []string
	for _, o := range c.options {
		out = append(out, o.label)
	}
	return out
}

func TestSetupChoosesSeveralModelsForClaudeCode(t *testing.T) {
	t.Parallel()
	run := chooseThreeClaudeModels(t)
	if got := optionLabels(
		run.asked[0],
	); !slices.Equal(
		got,
		[]string{"Recommended defaults", "Choose models"},
	) {
		t.Errorf("menu = %q", got)
	}
	list := run.asked[1]
	if !list.multi || !list.ordered || list.subtitle != claudeLimitNote || len(list.options) != 6 {
		t.Fatalf("model list = %+v", list)
	}
	if got := optionLabels(run.asked[2]); !slices.Equal(got,
		[]string{"gemini-4-neon", "gpt-6-astra", "deepseek-v4.1-flash"}) {
		t.Errorf("default list = %q", got)
	}
	path := filepath.Join(run.home, claude.SettingsFile)
	if got := readJSONKey(t, path, "model"); got != "gemini-4-neon" {
		t.Errorf("model = %v", got)
	}
	env := readJSONKey(t, path, "env").(map[string]any)
	for k, want := range map[string]string{
		"ANTHROPIC_CUSTOM_MODEL_OPTION":              "gemini-4-neon",
		"ANTHROPIC_DEFAULT_FABLE_MODEL":              "gpt-6-astra",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":               "deepseek-v4.1-flash",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":             "claude-sonnet-5-5",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":              "claude-haiku-5-5",
		"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1",
	} {
		if env[k] != want {
			t.Errorf("%s = %v, want %s", k, env[k], want)
		}
	}
	got := slices.Sorted(slices.Values(run.probes()["/v1/messages"]))
	if !slices.Equal(got, []string{"deepseek-v4.1-flash", "gemini-4-neon", "gpt-6-astra"}) {
		t.Errorf("Messages checks = %v, want one per pick", got)
	}
}

func TestClaudeDefaultListBackKeepsPicks(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, claudeMixCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	p := &inlineSetupPrompter{secret: "synthetic-key", scriptedSelector: scriptedSelector{
		answers: []selectAnswer{
			{picked: []int{1}},    // Choose models
			{picked: []int{5, 4}}, // gpt-6-astra, then gemini-4-neon
			{err: errBack},        // Esc on the default list
			{picked: []int{5, 4}}, // the same picks, still ticked
			{picked: []int{0}},    // default gpt-6-astra
			{picked: []int{1}},    // launch only
			{picked: []int{0}},    // Apply
		},
	}}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()
	if code := Run(t.Context(), []string{"setup", "claude"}, env, deps); code != 0 {
		t.Fatalf("setup exit %d: %s", code, stderr.String())
	}
	again := p.asked[3]
	if !again.options[4].picked || !again.options[5].picked ||
		!slices.Equal(again.order, []int{5, 4}) {
		t.Errorf("reopened list lost the picks: order %v, options %+v", again.order, again.options)
	}
	if got := readJSONKey(t, filepath.Join(home, claude.SettingsFile), "model"); got != "gpt-6-astra" {
		t.Errorf("model = %v, want gpt-6-astra", got)
	}
}

func TestClaudeModelListRefusesSixthModelFromAnotherVendor(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, claudeWideCatalogue)
	env, _ := inlineSetupEnv(t, srv.URL)
	p := &inlineSetupPrompter{secret: "synthetic-key", scriptedSelector: scriptedSelector{
		answers: []selectAnswer{{picked: []int{1}}, {err: errCancelled}},
	}}
	deps := inlineDeps(p, io.Discard)
	deps.HTTP = srv.Client()
	_ = Run(t.Context(), []string{"setup", "claude"}, env, deps)
	if len(p.asked) < 2 || p.asked[1].refuse == nil {
		t.Fatalf("asked = %+v", p.asked)
	}
	refuse := p.asked[1].refuse
	five := []bool{true, false, true, true, true, true, false} // qwen, deepseek, gemini, kimi, gpt
	if got := refuse(five, 6); got != claudeLimitRefusal {
		t.Errorf("sixth tick: %q, want the refusal", got)
	}
	if got := refuse(five, 1); got != "" {
		t.Errorf("Claude tick refused: %q", got)
	}
	if got := refuse([]bool{true, false, true, true, true, false, false}, 6); got != "" {
		t.Errorf("fifth tick refused: %q", got)
	}
}

func TestPlainClaudeModelListRefusesSixModelsFromOtherVendors(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, claudeWideCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	p := &plainSetupPrompter{secret: "synthetic-key", answers: []string{
		"2",             // Models for Claude Code: 1 Recommended defaults, 2 Choose models
		"1,2,3,4,5,6,7", // seven models, six from other vendors: refused
		"2,4",           // claude-sonnet-5-5 and gemini-4-neon
		"2",             // Default model for Claude Code: gemini-4-neon
		"n",             // Default provider: launch only
		"1",             // Review: Apply
	}}
	var stderr strings.Builder
	deps := Deps{
		Stderr:   &stderr,
		Stdout:   io.Discard,
		Prompter: p,
		Runner:   allInstalled{},
		HTTP:     srv.Client(),
	}
	if code := Run(t.Context(), []string{"setup", "claude", "--plain"}, env, deps); code != 0 {
		t.Fatalf("setup exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), claudePlainLimit) {
		t.Errorf("limit not stated:\n%s", stderr.String())
	}
	if got := readJSONKey(
		t,
		filepath.Join(home, claude.SettingsFile),
		"model",
	); got != "gemini-4-neon" {
		t.Errorf("model = %v, want gemini-4-neon", got)
	}
}

// claudeFableCatalogue grants Claude Fable 5.2 next to the three Claude tiers, listed as Anthropic 0 fable-5-2,
// 1 haiku, 2 opus, 3 sonnet; Google 4.
const claudeFableCatalogue = `[
	{"id":"claude-opus-5-5","owned_by":"Anthropic"},
	{"id":"claude-sonnet-5-5","owned_by":"Anthropic"},
	{"id":"claude-haiku-5-5","owned_by":"Anthropic"},
	{"id":"claude-fable-5-2","owned_by":"Anthropic"},
	{"id":"gemini-4-neon","owned_by":"Google"}
]`

// A ticked Claude model lands in the tier its alias names, ahead of that tier's preset model.
func TestSetupPlacesTickedClaudeModelInItsTier(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, claudeFableCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	p := &inlineSetupPrompter{secret: "synthetic-key", scriptedSelector: scriptedSelector{
		answers: []selectAnswer{
			{picked: []int{1}},    // Models for Claude Code: 0 Recommended defaults, 1 Choose models
			{picked: []int{0, 4}}, // claude-fable-5-2, then gemini-4-neon
			{picked: []int{0}},    // Default model for Claude Code: claude-fable-5-2
			{picked: []int{1}},    // Default provider: launch only
			{picked: []int{0}},    // Review: Apply
		},
	}}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()
	if code := Run(t.Context(), []string{"setup", "claude"}, env, deps); code != 0 {
		t.Fatalf("setup exit %d: %s", code, stderr.String())
	}
	path := filepath.Join(home, claude.SettingsFile)
	if got := readJSONKey(t, path, "model"); got != "claude-fable-5-2" {
		t.Errorf("model = %v, want claude-fable-5-2", got)
	}
	slots := readJSONKey(t, path, "env").(map[string]any)
	for k, want := range map[string]string{
		"ANTHROPIC_CUSTOM_MODEL_OPTION":  "gemini-4-neon",
		"ANTHROPIC_DEFAULT_FABLE_MODEL":  "claude-fable-5-2",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   "claude-opus-5-5",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-5-5",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "claude-haiku-5-5",
	} {
		if slots[k] != want {
			t.Errorf("%s = %v, want %s", k, slots[k], want)
		}
	}
}

func TestClaudeMenuNamesUngrantedCurrentModelLikeOtherTools(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, claudeMixCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"model":"retired-model"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &inlineSetupPrompter{secret: "synthetic-key", scriptedSelector: scriptedSelector{
		answers: []selectAnswer{{err: errCancelled}},
	}}
	deps := inlineDeps(p, io.Discard)
	deps.HTTP = srv.Client()
	_ = Run(t.Context(), []string{"setup", "claude"}, env, deps)
	menu := p.asked[0]
	want := []string{"Keep unchanged (refuse setup)", "Recommended defaults", "Choose models"}
	if got := optionLabels(menu); !slices.Equal(got, want) {
		t.Errorf("menu = %q, want %q", got, want)
	}
	if !strings.Contains(menu.title, "(current retired-model is not granted)") {
		t.Errorf("title = %q", menu.title)
	}
}

// An Aiven-owned claude-opus-5-5 is Aiven's model: shown under Aiven and counted toward the five.
func TestClaudeModelListCountsAivenOwnedClaudeAliasAsAnotherVendor(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, `[
		{"id":"claude-opus-5-5","owned_by":"Aiven"},
		{"id":"claude-sonnet-5-5","owned_by":"Anthropic"},
		{"id":"deepseek-v4.1-flash","owned_by":"DeepSeek"},
		{"id":"gemini-4-neon","owned_by":"Google"},
		{"id":"kimi-k3","owned_by":"Moonshot AI"},
		{"id":"gpt-6-astra","owned_by":"OpenAI"},
		{"id":"glm-5","owned_by":"Z.ai"}
	]`)
	env, _ := inlineSetupEnv(t, srv.URL)
	p := &inlineSetupPrompter{secret: "synthetic-key", scriptedSelector: scriptedSelector{
		answers: []selectAnswer{{picked: []int{1}}, {err: errCancelled}},
	}}
	deps := inlineDeps(p, io.Discard)
	deps.HTTP = srv.Client()
	_ = Run(t.Context(), []string{"setup", "claude"}, env, deps)
	list := p.asked[1]
	// Aiven 0 claude-opus-5-5, Anthropic 1, DeepSeek 2, Google 3, Moonshot AI 4, OpenAI 5, Z.ai 6.
	if list.options[0].label != "claude-opus-5-5" ||
		!strings.HasSuffix(list.options[0].detail, "Aiven") {
		t.Fatalf("first option = %+v", list.options[0])
	}
	five := []bool{true, false, true, true, true, true, false}
	if got := list.refuse(five, 6); got != claudeLimitRefusal {
		t.Errorf("sixth tick: %q, want the refusal", got)
	}
	if got := list.refuse(five, 1); got != "" {
		t.Errorf("Anthropic tick refused: %q", got)
	}
}

func TestClaudeModelListOffersRoutingAlias(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, `[
		{"id":"claude-sonnet-5-5","owned_by":"Anthropic"},
		{"id":"aiven-model","owned_by":"Aiven"}
	]`)
	env, _ := inlineSetupEnv(t, srv.URL)
	p := &inlineSetupPrompter{secret: "synthetic-key", scriptedSelector: scriptedSelector{
		answers: []selectAnswer{{picked: []int{1}}, {err: errCancelled}},
	}}
	deps := inlineDeps(p, io.Discard)
	deps.HTTP = srv.Client()
	_ = Run(t.Context(), []string{"setup", "claude"}, env, deps)
	if got := p.asked[0].options[1].detail; got != "pick from 2 granted models" {
		t.Errorf("detail = %q", got)
	}
	if got := optionLabels(
		p.asked[1],
	); !slices.Equal(
		got,
		[]string{"claude-sonnet-5-5", "aiven-model"},
	) {
		t.Errorf("model list = %q", got)
	}
}

// Typed ids are model ids, never harness names, and Back from the default list keeps them.
func TestPlainClaudeModelListTakesIdsAndBackKeepsPicks(t *testing.T) {
	t.Parallel()
	srv, _ := inlineGateway(t, claudeMixCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	p := &plainSetupPrompter{secret: "synthetic-key", answers: []string{
		"2",                         // Choose models
		"gemini-4-neon,gpt-6-astra", // ids, not numbers
		"b",                         // Back from the default list
		"",                          // Enter keeps the picks
		"2",                         // default gpt-6-astra
		"n",                         // launch only
		"1",                         // Apply
	}}
	var stderr strings.Builder
	deps := Deps{
		Stderr:   &stderr,
		Stdout:   io.Discard,
		Prompter: p,
		Runner:   allInstalled{},
		HTTP:     srv.Client(),
	}
	if code := Run(
		context.Background(),
		[]string{"setup", "claude", "--plain"},
		env,
		deps,
	); code != 0 {
		t.Fatalf("setup exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), claudeKeptNote+"gemini-4-neon, gpt-6-astra") {
		t.Errorf("kept picks not shown:\n%s", stderr.String())
	}
	path := filepath.Join(home, claude.SettingsFile)
	if got := readJSONKey(t, path, "model"); got != "gpt-6-astra" {
		t.Errorf("model = %v, want gpt-6-astra", got)
	}
	env2 := readJSONKey(t, path, "env").(map[string]any)
	if env2["ANTHROPIC_CUSTOM_MODEL_OPTION"] != "gemini-4-neon" {
		t.Errorf("custom option = %v", env2["ANTHROPIC_CUSTOM_MODEL_OPTION"])
	}
}

func TestParsePickOfferedTakesOfferedIdWhateverItIsCalled(t *testing.T) {
	t.Parallel()
	offered := []string{"gemini-4-neon", "gpt-6-astra", "pi"}
	got, ok := parsePickOffered("gemini-4-neon, 2,pi", offered)
	if !ok || !slices.Equal(got, []string{"gemini-4-neon", "gpt-6-astra", "pi"}) {
		t.Errorf("got %q, %v", got, ok)
	}
	if _, ok := parsePickOffered("codex", offered); ok {
		t.Error("an id that is not offered was taken")
	}
}

// sharedPresetRun picks the shared vendor preset at index preset with Pi and Claude Code selected
// and returns the Claude Code env block and top-level model.
func sharedPresetRun(
	t *testing.T,
	catalogue string,
	preset int,
) (map[string]any, any, *inlineSetupPrompter) {
	t.Helper()
	srv, _ := inlineGateway(t, catalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	p := &inlineSetupPrompter{secret: "synthetic-key", scriptedSelector: scriptedSelector{
		answers: []selectAnswer{
			{picked: []int{0, 3}}, // Harnesses: Pi and Claude Code
			{picked: []int{preset}},
			{picked: []int{1}}, // Default provider: launch only
			{picked: []int{0}}, // Review: Apply all
		},
	}}
	var stderr strings.Builder
	deps := inlineDeps(p, &stderr)
	deps.HTTP = srv.Client()
	if code := Run(t.Context(), []string{"setup"}, env, deps); code != 0 {
		t.Fatalf("setup exit %d: %s", code, stderr.String())
	}
	path := filepath.Join(home, claude.SettingsFile)
	return readJSONKey(t, path, "env").(map[string]any), readJSONKey(t, path, "model"), p
}

func TestSharedVendorPresetSetsClaudeCodeDefaultOnly(t *testing.T) {
	t.Parallel()
	env, model, p := sharedPresetRun(t, multiModelTestCatalogue, 3) // 0 Recommended, 3 Open-weight
	if got := p.asked[1].options[3]; got.label != "Open-weight" || got.detail != "glm-5" {
		t.Fatalf("Default model option 3 = %+v", got)
	}
	if model != "glm-5" || env["ANTHROPIC_CUSTOM_MODEL_OPTION"] != "glm-5" {
		t.Errorf("Claude Code default = %v, custom %v", model, env["ANTHROPIC_CUSTOM_MODEL_OPTION"])
	}
	for _, v := range []string{"ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL"} {
		if env[v] != "claude-sonnet-5-5" {
			t.Errorf("%s = %v, want claude-sonnet-5-5", v, env[v])
		}
	}
}

func TestSharedVendorPresetKeepsGrantedFableInItsTier(t *testing.T) {
	t.Parallel()
	const catalogue = `[
	{"id":"claude-sonnet-5-5","owned_by":"Anthropic"},
	{"id":"claude-fable-5-2","owned_by":"Anthropic"},
	{"id":"glm-5","owned_by":"Z.ai"}
]`
	env, model, p := sharedPresetRun(t, catalogue, 2) // 0 Recommended, 1 Anthropic, 2 Open-weight
	if got := p.asked[1].options[2]; got.label != "Open-weight" || got.detail != "glm-5" {
		t.Fatalf("Default model option 2 = %+v", got)
	}
	if model != "glm-5" || env["ANTHROPIC_CUSTOM_MODEL_OPTION"] != "glm-5" {
		t.Errorf("Claude Code default = %v, custom %v", model, env["ANTHROPIC_CUSTOM_MODEL_OPTION"])
	}
	if env["ANTHROPIC_DEFAULT_FABLE_MODEL"] != "claude-fable-5-2" {
		t.Errorf("fable tier = %v, want claude-fable-5-2", env["ANTHROPIC_DEFAULT_FABLE_MODEL"])
	}
	if env["ANTHROPIC_DEFAULT_SONNET_MODEL"] != "claude-sonnet-5-5" {
		t.Errorf("sonnet tier = %v", env["ANTHROPIC_DEFAULT_SONNET_MODEL"])
	}
}

func TestSharedSelectionLeavesRecommendedDefaultsAndOtherToolsAlone(t *testing.T) {
	t.Parallel()
	table := []models.Model{
		{Alias: "claude-sonnet-5-5", Owner: "Anthropic"},
		{Alias: "glm-5", Owner: "Z.ai"},
	}
	preset := harness.ModelSelection{Preset: models.PresetOpenWeight}
	if got := sharedSelection("claude", table, harness.ModelSelection{}); !got.IsZero() {
		t.Errorf("Recommended defaults for Claude Code = %+v, want zero (keep existing)", got)
	}
	if got := sharedSelection("pi", table, preset); got.Preset != preset.Preset || got.Model != "" {
		t.Errorf("preset for pi = %+v, want unchanged", got)
	}
	if got := sharedSelection("claude", table, preset); got.Preset != "" || got.Model != "glm-5" {
		t.Errorf("preset for claude = %+v, want Model glm-5 only", got)
	}
}

// A repeat --yes run leaves a chosen mix of models where it is.
func TestRepeatSetupKeepsChosenClaudeSlots(t *testing.T) {
	t.Parallel()
	run := chooseThreeClaudeModels(t)
	path := filepath.Join(run.home, claude.SettingsFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	deps := inlineDeps(&inlineSetupPrompter{}, &stderr)
	deps.HTTP = run.srv.Client()
	if code := Run(t.Context(), []string{"setup", "claude", "--yes"}, run.env, deps); code != 0 {
		t.Fatalf("repeat setup exit %d: %s", code, stderr.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) ||
		!strings.Contains(stderr.String(), "Already configured; no file needs a change.") {
		t.Errorf("repeat setup changed the slots:\n%s\nstderr:\n%s", after, stderr.String())
	}
}

// Model ids are matched as displayed, so a mixed-case id is chosen as typed and no word of the tool picker is a shortcut.
func TestPlainClaudePickerMatchesIdsExactly(t *testing.T) {
	t.Parallel()
	table := []models.Model{{Alias: "all"}, {Alias: "Vendor-Mixed"}, {Alias: "other"}}
	offered := []string{"all", "Vendor-Mixed", "other"}
	for answer, want := range map[string][]string{
		"Vendor-Mixed": {"Vendor-Mixed"},
		"all":          {"all"},
		"2, other":     {"Vendor-Mixed", "other"},
	} {
		deps := Deps{Prompter: &plainSetupPrompter{answers: []string{answer}}, Stderr: io.Discard}
		got, err := pickClaudeModelsPlain(
			deps,
			newPresenter(config.Env{}, deps, true),
			table,
			offered,
			nil,
		)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("answer %q picked %q, %v; want %q", answer, got, err, want)
		}
	}
	deps := Deps{Prompter: &plainSetupPrompter{answers: []string{"vendor-mixed"}}, Stderr: io.Discard}
	if got, err := pickClaudeModelsPlain(
		deps,
		newPresenter(config.Env{}, deps, true),
		table,
		offered,
		nil,
	); err == nil {
		t.Errorf("a differently cased id picked %q", got)
	}
}
