// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package claude_test

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

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

const (
	baseURL = "https://gateway.example"
	key     = "sk-test-valid"
)

func ptr(n int) *int { return &n }

// granted is a key's catalogue: Opus and GLM with 1M windows, and a GPT that states none.
func granted() []models.Model {
	return []models.Model{
		{Alias: "claude-opus", Name: "Claude Opus", Context: ptr(1_000_000)},
		{Alias: "glm", Context: ptr(1_000_000)},
		{Alias: "gpt-sol"},
	}
}

func input(t *testing.T) claude.Input {
	t.Helper()
	return claude.Input{
		Table:    granted(),
		BaseURL:  baseURL,
		UserHome: t.TempDir(),
		WorkDir:  t.TempDir(),
	}
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("%v in %s", err, data)
	}
	return v
}

// setUp writes the files Setup plans, as setup does after its preview.
func setUp(t *testing.T, in claude.Input) {
	t.Helper()
	plan, err := claude.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range plan.Files {
		write(t, f.Path, string(f.Data))
	}
}

func TestSetupWritesTheGatewaySettingsBesideTheUsersOwn(t *testing.T) {
	in := input(t)
	write(
		t,
		filepath.Join(in.UserHome, ".claude/settings.json"),
		`{"model":"opus","env":{"ANTHROPIC_API_KEY":"sk-ant-user"}}`,
	)
	path := filepath.Join(in.UserHome, claude.SettingsFile)
	write(
		t,
		path,
		`{"permissions":{"allow":["Bash(ls)"]},"env":{"FOO":"bar","CLAUDE_CODE_MAX_CONTEXT_TOKENS":"1"}}`,
	)
	plan, err := claude.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 || plan.Files[0].Path != path || !plan.Files[0].Backup ||
		plan.Argv != nil ||
		plan.Env != nil {
		t.Fatalf("plan = %+v", plan)
	}
	data := plan.Files[0].Data
	got := decode(t, data)
	wantEnv := map[string]any{
		"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1",
		"FOO":                             "bar",
		"ANTHROPIC_BASE_URL":              baseURL,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":    "claude-opus[1m]",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":  "claude-opus[1m]",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":   "claude-opus[1m]",
		"ANTHROPIC_DEFAULT_FABLE_MODEL":   "claude-opus[1m]",
		"ANTHROPIC_AUTH_TOKEN":            "",
		"ANTHROPIC_API_KEY":               "",
		"CLAUDE_CODE_USE_BEDROCK":         "",
		"CLAUDE_CODE_USE_VERTEX":          "",
		"CLAUDE_CODE_USE_FOUNDRY":         "",
		"AWS_BEARER_TOKEN_BEDROCK":        "",
		"ANTHROPIC_CUSTOM_HEADERS":        "",
		"CLAUDE_CODE_MAX_CONTEXT_TOKENS":  "",
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "",
	}
	if !reflect.DeepEqual(got["env"], wantEnv) || got["model"] != "claude-opus[1m]" ||
		got["apiKeyHelper"] != claude.APIKeyHelper(baseURL) ||
		!reflect.DeepEqual(got["permissions"], map[string]any{"allow": []any{"Bash(ls)"}}) {
		t.Errorf("settings = %v", got)
	}
	if strings.Contains(string(data), key) {
		t.Errorf("settings hold the key:\n%s", data)
	}
	if want := `"apiKeyHelper": "sh -c 'cat ~/` + creds.Rel(
		baseURL,
	) + `'"`; runtime.GOOS != "windows" &&
		!strings.Contains(string(data), want) {
		t.Errorf("apiKeyHelper not written as the shell reads it:\n%s", data)
	}
}

func TestSetupWiresTheUsersSettingsOnlyWhenAskedToMakeTheGatewayTheDefault(t *testing.T) {
	in := input(t)
	in.Default = true
	plan, err := claude.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(in.UserHome, claude.SettingsFile),
		filepath.Join(in.UserHome, ".claude/settings.json"),
	}
	var paths []string
	for _, f := range plan.Files {
		paths = append(paths, f.Path)
	}
	if !slices.Equal(paths, want) {
		t.Fatalf("files = %q, want %q", paths, want)
	}
	if got := decode(
		t,
		plan.Files[1].Data,
	); got["apiKeyHelper"] != claude.APIKeyHelper(baseURL) ||
		got["model"] != "claude-opus[1m]" {
		t.Errorf("settings.json = %v", got)
	}
}

func TestSetupKeepsAChosenModelAndDeclaresAWindowUnderOneMillion(t *testing.T) {
	in := input(t)
	in.Table = []models.Model{
		{Alias: "glm-fast", Context: ptr(262_144)},
		{Alias: "claude-opus", Context: ptr(1_000_000)},
	}
	plan, err := claude.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	got := decode(t, plan.Files[0].Data)
	if env, _ := got["env"].(map[string]any); env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "" ||
		env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] != "" ||
		got["model"] != "claude-opus[1m]" {
		t.Errorf("settings = %v", got)
	}
	write(t, filepath.Join(in.UserHome, claude.SettingsFile), string(plan.Files[0].Data))
	for _, tc := range []struct{ model, window string }{
		{"claude-opus", ""},
		{"claude-opus[1m]", ""},
		{"glm-fast", "262144"},
		{"opus", ""},
		{"not-granted", ""},
	} {
		path := filepath.Join(in.UserHome, claude.SettingsFile)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		saved := decode(t, data)
		saved["model"] = tc.model
		write(t, path, string(mustJSON(t, saved)))
		plan, err := claude.Setup(in)
		if err != nil {
			t.Fatal(err)
		}
		got := decode(t, plan.Files[0].Data)
		env, _ := got["env"].(map[string]any)
		if window, _ := env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"].(string); got["model"] != tc.model ||
			window != tc.window {
			t.Errorf(
				"chosen %s: model %v, window %q; want window %q",
				tc.model,
				got["model"],
				window,
				tc.window,
			)
		}
	}
}

// Claude Code runs env.ANTHROPIC_MODEL over model, so a kept one sets the window.
func TestSetupDeclaresTheWindowOfAKeptAnthropicModel(t *testing.T) {
	in := input(t)
	in.Table = []models.Model{
		{Alias: "glm-fast", Context: ptr(262_144)},
		{Alias: "deepseek-v3.2", Context: ptr(163_840)},
	}
	write(
		t,
		filepath.Join(in.UserHome, claude.SettingsFile),
		`{"model":"glm-fast","env":{"ANTHROPIC_MODEL":"deepseek-v3.2"}}`,
	)
	plan, err := claude.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	got := decode(t, plan.Files[0].Data)
	env, _ := got["env"].(map[string]any)
	if got["model"] != "glm-fast" || env["ANTHROPIC_MODEL"] != "deepseek-v3.2" ||
		env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "163840" {
		t.Errorf("settings = %v", got)
	}
}

// A `[1m]` kept from an earlier catalogue would make Claude Code run a model whose window has shrunk as 1M. Claude Code
// strips the suffix in any case and repeated; setup drops a stale one and never adds one to a choice.
func TestSetupDropsAOneMillionSuffixTheCatalogueNoLongerSupports(t *testing.T) {
	in := input(t)
	in.Table = []models.Model{
		{Alias: "glm-fast", Context: ptr(262_144)},
		{Alias: "claude-sonnet", Context: ptr(262_144)},
		{Alias: "claude-opus", Context: ptr(1_000_000)},
	}
	for _, tc := range []struct{ field, value, want string }{
		{"model", "glm-fast[1m]", "glm-fast"},
		{"model", "glm-fast[1M]", "glm-fast"},
		{"model", "glm-fast[1m][1m]", "glm-fast"},
		{"model", "sonnet[1m]", "sonnet"},
		{"model", "claude-opus[1m]", "claude-opus[1m]"},
		{"model", "claude-opus", "claude-opus"},
		{"model", "not-granted[1m]", "not-granted[1m]"},
		{"ANTHROPIC_MODEL", "glm-fast[1m]", "glm-fast"},
		{"ANTHROPIC_MODEL", "claude-opus[1M]", "claude-opus[1M]"},
	} {
		settings := map[string]any{"model": tc.value}
		if tc.field == "ANTHROPIC_MODEL" {
			settings = map[string]any{"env": map[string]any{tc.field: tc.value}}
		}
		write(t, filepath.Join(in.UserHome, claude.SettingsFile), string(mustJSON(t, settings)))
		plan, err := claude.Setup(in)
		if err != nil {
			t.Fatal(err)
		}
		got := decode(t, plan.Files[0].Data)
		if tc.field == "ANTHROPIC_MODEL" {
			got, _ = got["env"].(map[string]any)
		}
		if got[tc.field] != tc.want {
			t.Errorf("%s %s: kept %v, want %s", tc.field, tc.value, got[tc.field], tc.want)
		}
	}
}

// Selected names the catalogue model a session started from one settings file runs, through Claude Code's aliases.
func TestSelectedIsTheModelASettingsFileRuns(t *testing.T) {
	for _, tc := range []struct{ settings, want string }{
		{`{"model":"glm[1m]"}`, "glm"},
		{`{"model":"opus","env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"claude-opus[1m]"}}`, "claude-opus"},
		{`{"model":"glm","env":{"ANTHROPIC_MODEL":"Sonnet[1M]","ANTHROPIC_DEFAULT_SONNET_MODEL":"gpt-sol"}}`, "gpt-sol"},
		{`{"model":"glm","env":{"ANTHROPIC_MODEL":" "}}`, "glm"},
		{`{"model":"opusplan","env":{"ANTHROPIC_DEFAULT_SONNET_MODEL":"glm"}}`, "glm"},
	} {
		if got, err := claude.Selected([]byte(tc.settings)); err != nil || got != tc.want {
			t.Errorf("%s: %q, %v; want %q", tc.settings, got, err, tc.want)
		}
	}
	if _, err := claude.Selected([]byte(`[]`)); err == nil {
		t.Error("Selected read a settings file that is not an object")
	}
}

// The window follows the model a native --model runs, in either form and through Claude Code's aliases, and is left
// undeclared for a model the table does not hold.
func TestLaunchDeclaresTheWindowOfTheModelANativeArgumentRuns(t *testing.T) {
	in := input(t)
	in.Table = []models.Model{
		{Alias: "claude-sonnet-5-5", Context: ptr(1_000_000)},
		{Alias: "claude-opus-5-5", Context: ptr(500_000)},
		{Alias: "deepseek-v3.2", Context: ptr(163_840)},
	}
	setUp(t, in)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"--model", "deepseek-v3.2"}, "CLAUDE_CODE_MAX_CONTEXT_TOKENS=163840"},
		{[]string{"--model=deepseek-v3.2", "-p", "hi"}, "CLAUDE_CODE_MAX_CONTEXT_TOKENS=163840"},
		{[]string{"--model", "deepseek-v3.2", "--model=claude-opus-5-5"}, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=500000"},
		{[]string{"--model", "opus"}, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=500000"},
		{[]string{"--model", "opus[1m]"}, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=500000"},
		{[]string{"--model", "not-granted"}, ""},
		{[]string{"-p", "--", "--model", "deepseek-v3.2"}, ""},
		{[]string{"--model"}, ""},
	} {
		in.Args = tc.args
		plan, err := claude.Launch(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := windowIn(t, plan); got != tc.want {
			t.Errorf("%q: window %q, want %q", tc.args, got, tc.want)
		}
		if !slices.Equal(plan.Argv[3:], tc.args) {
			t.Errorf("%q: argv %q", tc.args, plan.Argv)
		}
	}
}

// Claude Code ignores a settings file holding comments or trailing commas, so they go and the layout stays.
func TestSetupStripsCommentsAndKeepsLayoutInBothSettingsFiles(t *testing.T) {
	in := input(t)
	in.Default = true
	const src = `{
    // the user's own note
    "permissions": { "allow": ["Bash(ls)"] }, /* kept inline */
    "env": {
        "FOO": "bar", // why FOO
        "ANTHROPIC_API_KEY": "sk-ant-user"
    },
}
`
	for _, name := range []string{claude.SettingsFile, ".claude/settings.json"} {
		write(t, filepath.Join(in.UserHome, name), src)
	}
	plan, err := claude.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range plan.Files {
		data := string(f.Data)
		for _, kept := range []string{"{\n    \"permissions\": { \"allow\": [\"Bash(ls)\"] },\n", "\n        \"FOO\": \"bar\",\n"} {
			if !strings.Contains(data, kept) {
				t.Errorf("%s lost %q:\n%s", f.Path, kept, data)
			}
		}
		if !json.Valid(f.Data) {
			t.Errorf("%s is not plain JSON:\n%s", f.Path, data)
		}
		write(t, f.Path, data)
		got, err := harness.ReadObject(f.Path)
		if err != nil {
			t.Fatal(err)
		}
		env, _ := got["env"].(map[string]any)
		if got["apiKeyHelper"] != claude.APIKeyHelper(baseURL) || env["ANTHROPIC_BASE_URL"] != baseURL ||
			env["ANTHROPIC_API_KEY"] != "" ||
			env["FOO"] != "bar" {
			t.Errorf("%s = %v", f.Path, got)
		}
	}
	again, err := claude.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range again.Files {
		if !slices.Equal(f.Data, plan.Files[i].Data) {
			t.Errorf("second setup changed %s:\n%s", f.Path, f.Data)
		}
	}
}

func TestSetupRefusesSettingsItCannotRead(t *testing.T) {
	for _, tc := range []struct{ name, file, data string }{
		{"env not an object", claude.SettingsFile, `{"env":[]}`},
		{"repeated keys", claude.SettingsFile, "{\n\"env\":{\"A\":\"1\",\"A\":\"2\"}\n}"},
		{"user settings malformed", ".claude/settings.json", `{"model":`},
		{"user env not an object", ".claude/settings.json", `{"env":"x"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input(t)
			write(t, filepath.Join(in.UserHome, tc.file), tc.data)
			if _, err := claude.Setup(in); err == nil {
				t.Fatal("Setup accepted unreadable settings")
			}
		})
	}
	in := input(t)
	write(t, filepath.Join(in.WorkDir, ".claude/settings.local.json"), `{"env":[]}`)
	if _, err := claude.Setup(in); err == nil {
		t.Error("Setup accepted unreadable project settings")
	}
	in = input(t)
	in.UserHome = ""
	if _, err := claude.Setup(in); err == nil || !errors.Is(err, config.ErrNoHome) {
		t.Errorf("Setup without a home: %v", err)
	}
}

// Claude Code runs the helper through /bin/sh. It reads its own gateway's key file and nothing else.
func TestAPIKeyHelperReadsOnlyItsGatewaysKeyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the helper is a POSIX shell command")
	}
	home := t.TempDir()
	// Only another gateway's key file first: the helper must not read it.
	if _, err := creds.Write(
		home,
		config.DefaultBaseURL,
		"sk-test-other",
		fsutil.WriteAtomic,
	); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, want string
		ok         bool
	}{{"environment", "", false}, {"key file", key + "\n", true}} {
		if tc.name == "key file" {
			if _, err := creds.Write(home, baseURL, key, fsutil.WriteAtomic); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("/bin/sh", "-c", claude.APIKeyHelper(baseURL))
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, creds.EnvVar + "=sk-test-env"}
		if out, err := cmd.Output(); (err == nil) != tc.ok || string(out) != tc.want {
			t.Errorf("%s: helper printed %q (%v), want %q", tc.name, out, err, tc.want)
		}
	}
}

func TestReadyAcceptsOnlyAivenWiringInEitherSettingsFile(t *testing.T) {
	in := input(t)
	if err := claude.Ready(in.UserHome, baseURL); err == nil {
		t.Error("Ready without settings")
	}
	setUp(t, in)
	if err := claude.Ready(in.UserHome, baseURL); err != nil {
		t.Errorf("Ready after setup: %v", err)
	}
	if err := claude.Ready(in.UserHome, "https://other.example"); err == nil {
		t.Error("Ready for another gateway")
	}
	settings := filepath.Join(in.UserHome, claude.SettingsFile)
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	write(
		t,
		settings,
		strings.ReplaceAll(string(data), creds.Rel(baseURL), creds.Rel(config.DefaultBaseURL)),
	)
	if err := claude.Ready(in.UserHome, baseURL); err == nil {
		t.Error("Ready with another gateway's key helper")
	}
	// The Aiven Console's helper falls back to the environment, which names no gateway.
	helper, _ := json.Marshal(claude.APIKeyHelper(baseURL))
	raw := "[ -f ~/" + creds.Rel(
		baseURL,
	) + " ] && cat ~/" + creds.Rel(
		baseURL,
	) + " || printf %s \"$" + creds.EnvVar + "\""
	if runtime.GOOS == "windows" {
		raw = "powershell.exe -NoProfile -NonInteractive -Command \"$p = Join-Path $env:USERPROFILE '" +
			strings.ReplaceAll(
				creds.Rel(baseURL),
				"/",
				"\\",
			) + "'; if (Test-Path -LiteralPath $p -PathType Leaf) { Get-Content -LiteralPath $p -Raw } else { $env:" + creds.EnvVar + " }\""
	}
	fallback, _ := json.Marshal(raw)
	write(t, settings, strings.Replace(string(data), string(helper), string(fallback), 1))
	if err := claude.Ready(in.UserHome, baseURL); err == nil {
		t.Error("Ready with a key helper that falls back to the environment")
	}
	write(t, settings, string(data))
	in = input(t)
	in.Default = true
	setUp(t, in)
	if err := os.Remove(filepath.Join(in.UserHome, claude.SettingsFile)); err != nil {
		t.Fatal(err)
	}
	if err := claude.Ready(in.UserHome, baseURL); err != nil {
		t.Errorf("Ready with only settings.json wired: %v", err)
	}
	write(
		t,
		filepath.Join(in.UserHome, ".claude/settings.json"),
		`{"apiKeyHelper":"cat ~/key","env":{"ANTHROPIC_BASE_URL":"`+baseURL+`"}}`,
	)
	if err := claude.Ready(in.UserHome, baseURL); err == nil {
		t.Error("Ready with another key helper")
	}
}

// Without a home, the relative paths would name the project's own .claude files in the working directory.
func TestReadyAndLaunchRefuseAMissingHome(t *testing.T) {
	in := input(t)
	setUp(t, in)
	t.Chdir(in.UserHome)
	in.UserHome = ""
	if err := claude.Ready("", baseURL); err == nil || !errors.Is(err, config.ErrNoHome) {
		t.Errorf("Ready without a home: %v", err)
	}
	if _, err := claude.Launch(in); err == nil || !errors.Is(err, config.ErrNoHome) {
		t.Errorf("Launch without a home: %v", err)
	}
}

func TestLaunchPassesOnlyLauncherOwnedSettingsAndTheKeyHelper(t *testing.T) {
	in := input(t)
	setUp(t, in)
	in.Args = []string{"-p", "hi"}
	plan, err := claude.Launch(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 0 || len(plan.Argv) != 5 || plan.Argv[0] != "claude" ||
		plan.Argv[1] != "--settings" ||
		!slices.Equal(plan.Argv[3:], in.Args) {
		t.Fatalf("plan = %+v", plan)
	}
	if strings.ContainsAny(plan.Argv[2], "\r\n") {
		t.Errorf(
			"inline settings span lines, which a Windows batch file cannot receive: %q",
			plan.Argv[2],
		)
	}
	inline := decode(t, []byte(plan.Argv[2]))
	env, _ := inline["env"].(map[string]any)
	if inline["apiKeyHelper"] != claude.APIKeyHelper(baseURL) ||
		inline["model"] != "claude-opus[1m]" ||
		env["ANTHROPIC_BASE_URL"] != baseURL ||
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "claude-opus[1m]" ||
		env["ANTHROPIC_AUTH_TOKEN"] != "" {
		t.Errorf("inline settings = %v", inline)
	}
	for _, kv := range plan.Env {
		if strings.Contains(kv, key) {
			t.Errorf("key in environment: %q", kv)
		}
	}
	if strings.Contains(strings.Join(plan.Argv, " "), key) {
		t.Errorf("key in argv: %q", plan.Argv)
	}
	for _, kv := range []string{
		"ANTHROPIC_MODEL=claude-opus[1m]", "ANTHROPIC_AUTH_TOKEN=", "ANTHROPIC_API_KEY=", "ANTHROPIC_DEFAULT_OPUS_MODEL_NAME=claude-opus (gateway)",
		"ANTHROPIC_DEFAULT_OPUS_MODEL_DESCRIPTION=Claude Opus via Aiven AI Gateway", "ANTHROPIC_CUSTOM_MODEL_OPTION=",
	} {
		if !slices.Contains(plan.Env, kv) {
			t.Errorf("env lacks %q: %q", kv, plan.Env)
		}
	}
}

func TestLaunchTakesTheModelFromSettingsThenUserAndProjectPins(t *testing.T) {
	in := input(t)
	setUp(t, in)
	write(t, filepath.Join(in.UserHome, claude.SettingsFile), `{"model":"claude-opus","apiKeyHelper":`+
		string(mustJSON(t, claude.APIKeyHelper(baseURL)))+`,"env":{"ANTHROPIC_BASE_URL":"`+baseURL+`"}}`)
	if plan, err := claude.Launch(
		in,
	); err != nil ||
		!slices.Contains(plan.Env, "ANTHROPIC_MODEL=claude-opus[1m]") {
		t.Errorf("settings model not used: %v, %q", err, plan.Env)
	}
	write(
		t,
		filepath.Join(in.UserHome, ".claude/settings.json"),
		`{"env":{"ANTHROPIC_DEFAULT_HAIKU_MODEL":"gpt-sol"}}`,
	)
	write(
		t,
		filepath.Join(in.WorkDir, ".claude/settings.json"),
		`{"model":"glm","env":{"ANTHROPIC_CUSTOM_MODEL_OPTION":"glm"}}`,
	)
	write(
		t,
		filepath.Join(in.WorkDir, ".claude/settings.local.json"),
		`{"env":{"ANTHROPIC_CUSTOM_MODEL_OPTION":"claude-opus[1m]"}}`,
	)
	plan, err := claude.Launch(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range []string{"ANTHROPIC_MODEL=glm[1m]", "ANTHROPIC_DEFAULT_HAIKU_MODEL=gpt-sol", "ANTHROPIC_CUSTOM_MODEL_OPTION=claude-opus[1m]"} {
		if !slices.Contains(plan.Env, kv) {
			t.Errorf("env lacks %q: %q", kv, plan.Env)
		}
	}
}

// Claude Code merges env apart from model, and env.ANTHROPIC_MODEL from any settings file outranks every file's model.
func TestLaunchRunsAPinnedAnthropicModelOverAProjectModel(t *testing.T) {
	in := input(t)
	in.Table = append(granted(), models.Model{Alias: "deepseek-v3.2", Context: ptr(163_840)})
	setUp(t, in)
	write(
		t,
		filepath.Join(in.UserHome, ".claude/settings.json"),
		`{"env":{"ANTHROPIC_MODEL":"deepseek-v3.2"}}`,
	)
	write(t, filepath.Join(in.WorkDir, ".claude/settings.json"), `{"model":"glm"}`)
	for _, tc := range []struct {
		args       []string
		model, win string
	}{
		{nil, "ANTHROPIC_MODEL=deepseek-v3.2", "CLAUDE_CODE_MAX_CONTEXT_TOKENS=163840"},
		{[]string{"--model", "glm"}, "ANTHROPIC_MODEL=deepseek-v3.2", ""},
	} {
		in.Args = tc.args
		plan, err := claude.Launch(in)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(plan.Env, tc.model) || windowIn(t, plan) != tc.win {
			t.Errorf(
				"%q: window %q, env %q; want %s and %q",
				tc.args,
				windowIn(t, plan),
				plan.Env,
				tc.model,
				tc.win,
			)
		}
	}
	// A blank ANTHROPIC_MODEL in a later file clears the earlier one, so the project's model runs.
	write(
		t,
		filepath.Join(in.WorkDir, ".claude/settings.json"),
		`{"model":"glm","env":{"ANTHROPIC_MODEL":""}}`,
	)
	in.Args = nil
	if plan, err := claude.Launch(
		in,
	); err != nil ||
		!slices.Contains(plan.Env, "ANTHROPIC_MODEL=glm[1m]") {
		t.Errorf("blank ANTHROPIC_MODEL did not clear the user's: %v, %q", err, plan.Env)
	}
	// The dedicated file's own env.ANTHROPIC_MODEL counts too.
	in = input(t)
	in.Table = append(granted(), models.Model{Alias: "deepseek-v3.2", Context: ptr(163_840)})
	write(
		t,
		filepath.Join(in.UserHome, claude.SettingsFile),
		`{"model":"glm","env":{"ANTHROPIC_MODEL":"deepseek-v3.2"}}`,
	)
	setUp(t, in)
	if plan, err := claude.Launch(
		in,
	); err != nil ||
		!slices.Contains(plan.Env, "ANTHROPIC_MODEL=deepseek-v3.2") {
		t.Errorf("dedicated ANTHROPIC_MODEL not run: %v, %q", err, plan.Env)
	}
}

func TestSetupAndLaunchTreatABlankModelAsNoChoice(t *testing.T) {
	in := input(t)
	path := filepath.Join(in.UserHome, claude.SettingsFile)
	write(t, path, `{"model":"[1m]"}`)
	plan, err := claude.Setup(in)
	if err != nil || decode(t, plan.Files[0].Data)["model"] != "claude-opus[1m]" {
		t.Errorf("blank model kept by setup: %v, %s", err, plan.Files[0].Data)
	}
	// Setup sets the model only when the file has none; one it cannot read as a name is the user's to fix.
	write(t, path, `{"model":5}`)
	if plan, err := claude.Setup(
		in,
	); err != nil ||
		decode(t, plan.Files[0].Data)["model"] == "claude-opus[1m]" {
		t.Errorf("non-string model replaced by setup: %v, %s", err, plan.Files[0].Data)
	}
	write(
		t,
		path,
		`{"model":"","apiKeyHelper":`+string(
			mustJSON(t, claude.APIKeyHelper(baseURL)),
		)+`,"env":{"ANTHROPIC_BASE_URL":"`+baseURL+`"}}`,
	)
	if plan, err := claude.Launch(
		in,
	); err != nil ||
		!slices.Contains(plan.Env, "ANTHROPIC_MODEL=claude-opus[1m]") {
		t.Errorf("blank model used by launch: %v, %q", err, plan.Env)
	}
}

// The inline settings outrank every native file, so each pin must reach them as well as the child environment.
func TestLaunchCarriesUsablePinsIntoTheInlineSettings(t *testing.T) {
	for _, tc := range []struct {
		name, shared, local string
		want                map[string]string
	}{
		{"unusable pins", `{"model":5}`,
			`{"env":{"ANTHROPIC_MODEL":5,"ANTHROPIC_DEFAULT_OPUS_MODEL":["gpt-sol"],"ANTHROPIC_DEFAULT_HAIKU_MODEL":null,"ANTHROPIC_DEFAULT_SONNET_MODEL":"[1m]"}}`,
			map[string]string{"ANTHROPIC_MODEL": "claude-opus[1m]", "ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus[1m]", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "claude-opus[1m]", "ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-opus[1m]"}},
		{"blank local clears shared", `{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"gpt-sol[1m]"}}`, `{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":""}}`,
			map[string]string{"ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus[1m]"}},
		{"pin not granted", `{}`, `{"env":{"ANTHROPIC_DEFAULT_SONNET_MODEL":"ghost-9[1m]"}}`,
			map[string]string{"ANTHROPIC_DEFAULT_SONNET_MODEL": "ghost-9"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input(t)
			setUp(t, in)
			write(t, filepath.Join(in.WorkDir, ".claude/settings.json"), tc.shared)
			write(t, filepath.Join(in.WorkDir, ".claude/settings.local.json"), tc.local)
			plan, err := claude.Launch(in)
			if err != nil {
				t.Fatal(err)
			}
			env, _ := decode(t, []byte(plan.Argv[2]))["env"].(map[string]any)
			for k, v := range tc.want {
				if !slices.Contains(plan.Env, k+"="+v) || env[k] != v {
					t.Errorf("%s = %q wanted in env %q and inline %v", k, v, plan.Env, env)
				}
			}
		})
	}
	in := input(t)
	setUp(t, in)
	write(
		t,
		filepath.Join(in.WorkDir, ".claude/settings.local.json"),
		`{"env":{"ANTHROPIC_DEFAULT_SONNET_MODEL":"ghost-9"}}`,
	)
	plan, err := claude.Launch(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range []string{"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME=ghost-9 (gateway)", "ANTHROPIC_DEFAULT_SONNET_MODEL_DESCRIPTION=ghost-9 via Aiven AI Gateway"} {
		if !slices.Contains(plan.Env, kv) {
			t.Errorf("env lacks %q: %q", kv, plan.Env)
		}
	}
}

// Below 1M the window is declared with the variable Claude Code reads for the id: it ignores
// CLAUDE_CODE_MAX_CONTEXT_TOKENS for its own model ids.
func TestLaunchDeclaresTheMainWindowOnlyUnderOneMillion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table []models.Model
		want  string
	}{
		{"262K main", []models.Model{{Alias: "glm-fast", Context: ptr(262_144)}}, "CLAUDE_CODE_MAX_CONTEXT_TOKENS=262144"},
		{"200K non-claude main", []models.Model{{Alias: "glm-4.7-flash", Context: ptr(200_000)}}, "CLAUDE_CODE_MAX_CONTEXT_TOKENS=200000"},
		{"500K gateway alias", []models.Model{{Alias: "claude-opus", Context: ptr(500_000)}}, "CLAUDE_CODE_MAX_CONTEXT_TOKENS=500000"},
		{"200K recognised main", []models.Model{{Alias: "claude-haiku-4-5", Context: ptr(200_000)}}, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=200000"},
		{"500K recognised main", []models.Model{{Alias: "claude-opus-4-8", Context: ptr(500_000)}}, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=500000"},
		{"500K dated recognised main", []models.Model{{Alias: "claude-sonnet-5-5-20260301", Context: ptr(500_000)}}, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=500000"},
		{"1M main", granted(), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input(t)
			in.Table = tc.table
			setUp(t, in)
			plan, err := claude.Launch(in)
			if err != nil {
				t.Fatal(err)
			}
			if got := windowIn(t, plan); got != tc.want {
				t.Errorf("window = %q, want %q", got, tc.want)
			}
		})
	}
}

// windowIn returns the window variable plan declares as NAME=value, failing unless the child env and the inline
// settings declare the same one, alone.
func windowIn(t *testing.T, plan harness.Plan) string {
	t.Helper()
	inline, _ := decode(t, []byte(plan.Argv[2]))["env"].(map[string]any)
	var set []string
	for _, name := range []string{"CLAUDE_CODE_MAX_CONTEXT_TOKENS", "CLAUDE_CODE_AUTO_COMPACT_WINDOW"} {
		got, inEnv := "", false
		for _, kv := range plan.Env {
			if v, ok := strings.CutPrefix(kv, name+"="); ok {
				got, inEnv = v, true
			}
		}
		v, inInline := inline[name].(string)
		if !inEnv || !inInline || v != got {
			t.Fatalf(
				"%s = %q (set %v) in env, %q (set %v) inline; want it set in both, blank unless active",
				name,
				got,
				inEnv,
				v,
				inInline,
			)
		}
		if got != "" {
			set = append(set, name+"="+got)
		}
	}
	if len(set) > 1 {
		t.Fatalf("both window variables set: %q", set)
	}
	return strings.Join(set, "")
}

func TestLaunchRefusesMissingWiringAndUnreadablePins(t *testing.T) {
	in := input(t)
	if _, err := claude.Launch(in); err == nil {
		t.Error("Launch before setup")
	}
	setUp(t, in)
	write(t, filepath.Join(in.WorkDir, ".claude/settings.json"), `{"env":[]}`)
	if _, err := claude.Launch(in); err == nil {
		t.Error("Launch with unreadable project settings")
	}
}

func TestEffectiveResolvesLayeredModelAndSelectingPath(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		planned                       string
		userSettings, proj, projLocal string
		wantRel, wantAlias            string
		wantInWorkDir                 bool
	}{
		{
			name:         "empty and permissions-only user settings keep planned model",
			planned:      `{"model":"glm[1m]"}`,
			userSettings: `{"permissions":{"allow":["Bash(ls)"]}}`,
			wantRel:      claude.SettingsFile,
			wantAlias:    "glm",
		},
		{
			name:         "user settings overrides dedicated model",
			planned:      `{"model":"glm[1m]"}`,
			userSettings: `{"model":"claude-retired"}`,
			wantRel:      ".claude/settings.json",
			wantAlias:    "claude-retired",
		},
		{
			name:          "project shared and local override user model",
			planned:       `{"model":"glm[1m]"}`,
			userSettings:  `{"model":"claude-opus"}`,
			proj:          `{"model":"gpt-sol"}`,
			projLocal:     `{"model":"claude-retired[1m]"}`,
			wantRel:       ".claude/settings.local.json",
			wantAlias:     "claude-retired",
			wantInWorkDir: true,
		},
		{
			name:         "user ANTHROPIC_MODEL outranks project model",
			planned:      `{"model":"glm[1m]"}`,
			userSettings: `{"env":{"ANTHROPIC_MODEL":"claude-retired"}}`,
			proj:         `{"model":"glm"}`,
			wantRel:      ".claude/settings.json",
			wantAlias:    "claude-retired",
		},
		{
			name:          "blank ANTHROPIC_MODEL in local layer clears earlier pin",
			planned:       `{"model":"glm[1m]"}`,
			userSettings:  `{"env":{"ANTHROPIC_MODEL":"claude-retired"}}`,
			proj:          `{"model":"claude-opus"}`,
			projLocal:     `{"env":{"ANTHROPIC_MODEL":""}}`,
			wantRel:       ".claude/settings.json",
			wantAlias:     "claude-opus",
			wantInWorkDir: true,
		},
		{
			name:         "native alias resolves through its slot",
			planned:      `{"model":"glm[1m]"}`,
			userSettings: `{"model":"opus"}`,
			wantRel:      ".claude/settings.json",
			wantAlias:    "claude-opus",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input(t)
			aivenPath := filepath.Join(in.UserHome, claude.SettingsFile)
			planned := map[string][]byte{aivenPath: []byte(tc.planned)}
			if tc.userSettings != "" {
				write(t, filepath.Join(in.UserHome, ".claude/settings.json"), tc.userSettings)
			}
			if tc.proj != "" {
				write(t, filepath.Join(in.WorkDir, ".claude/settings.json"), tc.proj)
			}
			if tc.projLocal != "" {
				write(t, filepath.Join(in.WorkDir, ".claude/settings.local.json"), tc.projLocal)
			}
			gotPath, gotAlias, gotSetting, err := claude.Effective(
				in.Table,
				in.UserHome,
				in.WorkDir,
				planned,
			)
			if err != nil {
				t.Fatal(err)
			}
			base := in.UserHome
			if tc.wantInWorkDir {
				base = in.WorkDir
			}
			wantPath := filepath.Join(base, tc.wantRel)
			if gotPath != wantPath || gotAlias != tc.wantAlias || gotSetting == "" {
				t.Errorf(
					"Effective = (%q, %q, %q), want (%q, %q, non-empty)",
					gotPath,
					gotAlias,
					gotSetting,
					wantPath,
					tc.wantAlias,
				)
			}
		})
	}
	t.Run("missing home fails", func(t *testing.T) {
		if _, _, _, err := claude.Effective(granted(), "", "", nil); !errors.Is(err, config.ErrNoHome) {
			t.Errorf("err = %v, want ErrNoHome", err)
		}
	})
	t.Run("empty planned falls back to catalogue default", func(t *testing.T) {
		in := input(t)
		write(t, filepath.Join(in.UserHome, ".claude/settings.json"), `{}`)
		_, alias, setting, err := claude.Effective(in.Table, in.UserHome, "", nil)
		if err != nil || alias != "claude-opus" || setting != "" {
			t.Errorf("Effective = (%q, %q, %v); want (claude-opus, \"\", nil)", alias, setting, err)
		}
	})
	t.Run("explicit dedicated choice overrides ungranted user ANTHROPIC_MODEL", func(t *testing.T) {
		in := input(t)
		in.Table = []models.Model{
			{Alias: "glm-5", Context: ptr(1_000_000)},
			{Alias: "glm-4.7-flash", Context: ptr(200_000)},
		}
		write(
			t,
			filepath.Join(in.UserHome, ".claude/settings.json"),
			`{"env":{"ANTHROPIC_MODEL":"claude-retired"}}`,
		)
		in.Default = false
		in.Selection = harness.ModelSelection{Preset: models.PresetOpenWeight}
		plan, err := claude.Setup(in)
		if err != nil {
			t.Fatal(err)
		}
		write(t, plan.Files[0].Path, string(plan.Files[0].Data))
		dedicated := decode(t, plan.Files[0].Data)
		env, _ := dedicated["env"].(map[string]any)
		if dedicated["model"] != "glm-5[1m]" || env["ANTHROPIC_MODEL"] != "" {
			t.Fatalf("dedicated settings = %v, want model=glm-5[1m] and blank ANTHROPIC_MODEL", dedicated)
		}
		planned := map[string][]byte{plan.Files[0].Path: plan.Files[0].Data}
		_, alias, _, err := claude.Effective(in.Table, in.UserHome, in.WorkDir, planned)
		if err != nil || alias != "glm-5" {
			t.Fatalf("Effective = %q, %v; want glm-5", alias, err)
		}
		lp, err := claude.Launch(in)
		if err != nil {
			t.Fatalf("Launch: %v", err)
		}
		if !slices.Contains(lp.Env, "ANTHROPIC_MODEL=glm-5[1m]") {
			t.Errorf("Launch env = %v, want ANTHROPIC_MODEL=glm-5[1m]", lp.Env)
		}
	})

	t.Run(
		"mixed granted user model plus ungranted user env lets explicit replacement win",
		func(t *testing.T) {
			in := input(t)
			in.Table = []models.Model{
				{Alias: "gpt-6.1-sol", Context: ptr(200_000)},
				{Alias: "glm-5", Context: ptr(1_000_000)},
				{Alias: "glm-4.7-flash", Context: ptr(200_000)},
			}
			write(
				t,
				filepath.Join(in.UserHome, ".claude/settings.json"),
				`{"model":"gpt-6.1-sol","env":{"ANTHROPIC_MODEL":"claude-retired"}}`,
			)
			in.Default = false
			in.Selection = harness.ModelSelection{Preset: models.PresetOpenWeight}
			plan, err := claude.Setup(in)
			if err != nil {
				t.Fatal(err)
			}
			write(t, plan.Files[0].Path, string(plan.Files[0].Data))
			planned := map[string][]byte{plan.Files[0].Path: plan.Files[0].Data}
			_, alias, _, err := claude.Effective(in.Table, in.UserHome, in.WorkDir, planned)
			if err != nil || alias != "glm-5" {
				t.Fatalf("Effective = %q, %v; want glm-5", alias, err)
			}
			lp, err := claude.Launch(in)
			if err != nil {
				t.Fatalf("Launch: %v", err)
			}
			if !slices.Contains(lp.Env, "ANTHROPIC_MODEL=glm-5[1m]") {
				t.Errorf("Launch env = %v, want ANTHROPIC_MODEL=glm-5[1m]", lp.Env)
			}
		},
	)

	t.Run(
		"explicit launcher-only choice overrides granted user model while project still wins",
		func(t *testing.T) {
			in := input(t)
			in.Table = []models.Model{
				{Alias: "claude-sonnet-5-5"},
				{Alias: "glm-5"},
				{Alias: "gpt-6.1-sol"},
			}
			write(t, filepath.Join(in.UserHome, ".claude/settings.json"), `{"model":"glm-5"}`)
			in.Default = false
			in.Selection = harness.ModelSelection{Preset: models.PresetOpenAI}
			plan, err := claude.Setup(in)
			if err != nil {
				t.Fatal(err)
			}
			write(t, plan.Files[0].Path, string(plan.Files[0].Data))
			planned := map[string][]byte{plan.Files[0].Path: plan.Files[0].Data}
			gotPath, gotAlias, gotSetting, err := claude.Effective(
				in.Table,
				in.UserHome,
				in.WorkDir,
				planned,
			)
			if err != nil || gotPath != plan.Files[0].Path || gotAlias != "gpt-6.1-sol" ||
				gotSetting != "model" {
				t.Fatalf(
					"Effective = (%q, %q, %q, %v), want (%q, gpt-6.1-sol, model, nil)",
					gotPath,
					gotAlias,
					gotSetting,
					err,
					plan.Files[0].Path,
				)
			}
			lp, err := claude.Launch(in)
			if err != nil {
				t.Fatalf("Launch: %v", err)
			}
			if !slices.Contains(lp.Env, "ANTHROPIC_MODEL=gpt-6.1-sol") {
				t.Errorf("Launch env = %v, want ANTHROPIC_MODEL=gpt-6.1-sol", lp.Env)
			}
			// Project override remains authoritative over dedicated launcher-only choice.
			projPath := filepath.Join(in.WorkDir, ".claude/settings.json")
			write(t, projPath, `{"model":"claude-sonnet-5-5"}`)
			gotPath, gotAlias, _, err = claude.Effective(in.Table, in.UserHome, in.WorkDir, planned)
			if err != nil || gotPath != projPath || gotAlias != "claude-sonnet-5-5" {
				t.Fatalf("Effective with project = (%q, %q, %v), want project override", gotPath, gotAlias, err)
			}
		},
	)

	t.Run(
		"ProjectUngranted detects hidden project ungranted model despite user env pin",
		func(t *testing.T) {
			in := input(t)
			in.Table = []models.Model{{Alias: "glm-5", Context: ptr(1_000_000)}}
			write(
				t,
				filepath.Join(in.UserHome, ".claude/settings.json"),
				`{"env":{"ANTHROPIC_MODEL":"claude-retired"}}`,
			)
			projLocal := filepath.Join(in.WorkDir, ".claude/settings.local.json")
			write(t, projLocal, `{"model":"project-retired"}`)
			gotPath, gotAlias, gotSetting, ok := claude.ProjectUngranted(
				in.Table,
				in.UserHome,
				in.WorkDir,
			)
			if !ok || gotPath != projLocal || gotAlias != "project-retired" || gotSetting != "model" {
				t.Fatalf(
					"ProjectUngranted = (%q, %q, %q, %v), want (%q, project-retired, model, true)",
					gotPath,
					gotAlias,
					gotSetting,
					ok,
					projLocal,
				)
			}
		},
	)

	t.Run(
		"ProjectUngranted excludes user settings.json when WorkDir is UserHome but keeps settings.local.json",
		func(t *testing.T) {
			in := input(t)
			in.WorkDir = in.UserHome
			in.Table = []models.Model{{Alias: "claude-sonnet-5-5"}}
			write(t, filepath.Join(in.UserHome, ".claude/settings.json"), `{"model":"retired-model"}`)
			if p, a, s, ok := claude.ProjectUngranted(in.Table, in.UserHome, in.WorkDir); ok {
				t.Fatalf("ProjectUngranted from home = (%q, %q, %q, true), want false", p, a, s)
			}
			localPath := filepath.Join(in.UserHome, ".claude/settings.local.json")
			write(t, localPath, `{"model":"local-retired"}`)
			gotPath, gotAlias, gotSetting, ok := claude.ProjectUngranted(
				in.Table,
				in.UserHome,
				in.WorkDir,
			)
			if !ok || gotPath != localPath || gotAlias != "local-retired" || gotSetting != "model" {
				t.Fatalf(
					"ProjectUngranted(local in home) = (%q, %q, %q, %v), want (%q, local-retired, model, true)",
					gotPath,
					gotAlias,
					gotSetting,
					ok,
					localPath,
				)
			}
		},
	)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSetupWithPresetReplacesModelAndSlotsAndRecomputesWindow(t *testing.T) {
	t.Parallel()
	in := input(t)
	in.Table = []models.Model{
		{Alias: "claude-sonnet-5-5", Context: ptr(1_000_000)},
		{Alias: "glm-5", Context: ptr(1_000_000)},
		{Alias: "glm-4.7-flash", Context: ptr(200_000)},
	}
	write(t, filepath.Join(in.UserHome, claude.SettingsFile), `{
		"permissions": {"allow": ["Bash(ls)"]},
		"model": "claude-retired",
		"env": {"FOO": "bar", "ANTHROPIC_MODEL": "claude-retired[1m]"}
	}`)
	in.Selection = harness.ModelSelection{Preset: models.PresetOpenWeight}
	plan, err := claude.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	got := decode(t, plan.Files[0].Data)
	env, _ := got["env"].(map[string]any)
	if got["model"] != "glm-5[1m]" || env["ANTHROPIC_MODEL"] != "glm-5[1m]" ||
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "glm-5[1m]" ||
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] != "glm-5[1m]" ||
		env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "glm-4.7-flash" ||
		env["ANTHROPIC_DEFAULT_FABLE_MODEL"] != "glm-5[1m]" ||
		env["ANTHROPIC_CUSTOM_MODEL_OPTION"] != nil ||
		env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "" ||
		env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] != "" ||
		env["FOO"] != "bar" {
		t.Fatalf("settings = %v", got)
	}
	if !reflect.DeepEqual(got["permissions"], map[string]any{"allow": []any{"Bash(ls)"}}) {
		t.Errorf("lost permissions: %v", got["permissions"])
	}
}

func TestSetupWithCustomModelPreservesClaudeTiersAndClearsStaleCustomOption(t *testing.T) {
	t.Parallel()
	in := input(t)
	in.Table = []models.Model{
		{Alias: "claude-opus-5-5", Context: ptr(1_000_000)},
		{Alias: "claude-sonnet-5-5", Context: ptr(1_000_000)},
		{Alias: "claude-haiku-5-5", Context: ptr(200_000)},
		{Alias: "deepseek-v3.2", Context: ptr(163_840)},
		{Alias: "gpt-6.1-sol"},
	}
	write(
		t,
		filepath.Join(in.UserHome, claude.SettingsFile),
		`{"model":"claude-sonnet-5-5[1m]","env":{"KEEP":"1","ANTHROPIC_CUSTOM_MODEL_OPTION":"gpt-6.1-sol[1m]"}}`,
	)
	in.Selection = harness.ModelSelection{Model: "deepseek-v3.2"}
	plan, err := claude.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	got := decode(t, plan.Files[0].Data)
	env, _ := got["env"].(map[string]any)
	if got["model"] != "deepseek-v3.2" ||
		env["ANTHROPIC_MODEL"] != nil ||
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "claude-opus-5-5[1m]" ||
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] != "claude-sonnet-5-5[1m]" ||
		env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "claude-haiku-5-5" ||
		env["ANTHROPIC_DEFAULT_FABLE_MODEL"] != "claude-opus-5-5[1m]" ||
		env["ANTHROPIC_CUSTOM_MODEL_OPTION"] != "deepseek-v3.2" ||
		env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "163840" ||
		env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] != "" ||
		env["KEEP"] != "1" {
		t.Fatalf("settings = %v", got)
	}

	// Recognised Claude id not in default tiers uses custom slot and CLAUDE_CODE_AUTO_COMPACT_WINDOW.
	in.Table = append(in.Table, models.Model{Alias: "claude-opus-4-8", Context: ptr(500_000)})
	in.Selection = harness.ModelSelection{Model: "claude-opus-4-8"}
	plan, err = claude.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	got = decode(t, plan.Files[0].Data)
	env, _ = got["env"].(map[string]any)
	if got["model"] != "claude-opus-4-8" ||
		env["ANTHROPIC_CUSTOM_MODEL_OPTION"] != "claude-opus-4-8" ||
		env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] != "500000" ||
		env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "" {
		t.Fatalf("recognised custom model settings = %v", got)
	}

	// Picking a Claude model already in a tier slot removes any stale ANTHROPIC_CUSTOM_MODEL_OPTION.
	in.Selection = harness.ModelSelection{Model: "claude-sonnet-5-5"}
	plan, err = claude.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	got = decode(t, plan.Files[0].Data)
	env, _ = got["env"].(map[string]any)
	if got["model"] != "claude-sonnet-5-5[1m]" || env["ANTHROPIC_CUSTOM_MODEL_OPTION"] != nil {
		t.Fatalf("expected stale custom option removed, got = %v", got)
	}

	// Switching to a preset also deletes a leftover ANTHROPIC_CUSTOM_MODEL_OPTION from disk.
	in.Selection = harness.ModelSelection{Preset: models.PresetAnthropic}
	plan, err = claude.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	got = decode(t, plan.Files[0].Data)
	env, _ = got["env"].(map[string]any)
	if env["ANTHROPIC_CUSTOM_MODEL_OPTION"] != nil {
		t.Fatalf("expected preset to delete stale custom option, got = %v", got)
	}

	// Open-weight-only key (no Anthropic models granted): custom model fills tier slots cleanly.
	in.Table = []models.Model{
		{Alias: "deepseek-v4.1-flash", Context: ptr(128_000)},
		{Alias: "glm-5.3", Context: ptr(200_000)},
	}
	in.Selection = harness.ModelSelection{Model: "glm-5.3"}
	plan, err = claude.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	got = decode(t, plan.Files[0].Data)
	env, _ = got["env"].(map[string]any)
	if got["model"] != "glm-5.3" ||
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "glm-5.3" ||
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] != "glm-5.3" ||
		env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "glm-5.3" ||
		env["ANTHROPIC_DEFAULT_FABLE_MODEL"] != "glm-5.3" ||
		env["ANTHROPIC_CUSTOM_MODEL_OPTION"] != nil {
		t.Fatalf("open-weight-only custom model settings = %v", got)
	}
}

func TestSetupCustomModelPreservesExistingGrantedClaudeTiers(t *testing.T) {
	t.Parallel()
	in := input(t)
	in.Table = []models.Model{
		{Alias: "claude-sonnet-4-5", Owner: "Anthropic"},
		{Alias: "claude-sonnet-5-5", Owner: "Anthropic"},
		{Alias: "glm-5", Owner: "Z.ai"},
	}
	write(
		t,
		filepath.Join(in.UserHome, claude.SettingsFile),
		`{"env":{"ANTHROPIC_DEFAULT_SONNET_MODEL":"claude-sonnet-4-5","ANTHROPIC_DEFAULT_OPUS_MODEL":"claude-retired"}}`,
	)
	in.Selection = harness.ModelSelection{Model: "glm-5"}
	plan, err := claude.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	got := decode(t, plan.Files[0].Data)
	env, _ := got["env"].(map[string]any)
	if got["model"] != "glm-5" ||
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] != "claude-sonnet-4-5" ||
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "claude-sonnet-5-5" ||
		env["ANTHROPIC_CUSTOM_MODEL_OPTION"] != "glm-5" {
		t.Fatalf("custom pick tier preservation = %v", got)
	}
}

func TestSetupPresetClearsInheritedUserCustomModelOptionOnLaunch(t *testing.T) {
	t.Parallel()
	in := input(t)
	in.Table = []models.Model{{Alias: "claude-sonnet-5-5"}}
	write(
		t,
		filepath.Join(in.UserHome, ".claude/settings.json"),
		`{"env":{"ANTHROPIC_CUSTOM_MODEL_OPTION":"retired-model"}}`,
	)
	in.Default = false
	in.Selection = harness.ModelSelection{Preset: models.PresetAnthropic}
	plan, err := claude.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range plan.Files {
		write(t, f.Path, string(f.Data))
	}
	lp, err := claude.Launch(in)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(lp.Env, "ANTHROPIC_CUSTOM_MODEL_OPTION=retired-model") {
		t.Errorf("launch reintroduced retired user custom option: %v", lp.Env)
	}
	if !slices.Contains(lp.Env, "ANTHROPIC_CUSTOM_MODEL_OPTION=") {
		t.Errorf("launch missing cleared custom option: %v", lp.Env)
	}
}

const discoveryVar = "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"

func TestSetupTurnsOnGatewayModelDiscoveryForAChoiceOrAFreshFile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, existing string
		sel            harness.ModelSelection
		want           any
	}{
		{name: "fresh file", want: "1"},
		{name: "keep current", existing: `{"model":"glm"}`, want: nil},
		{
			name:     "chosen models",
			existing: `{"model":"glm"}`,
			sel:      harness.ModelSelection{Model: "glm", Models: []string{"glm", "gpt-sol"}},
			want:     "1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := input(t)
			if tc.existing != "" {
				write(t, filepath.Join(in.UserHome, claude.SettingsFile), tc.existing)
			}
			in.Selection = tc.sel
			plan, err := claude.Setup(in)
			if err != nil {
				t.Fatal(err)
			}
			env, _ := decode(t, plan.Files[0].Data)["env"].(map[string]any)
			if got := env[discoveryVar]; got != tc.want {
				t.Errorf("%s = %v, want %v", discoveryVar, got, tc.want)
			}
		})
	}
}

func TestLaunchCarriesChosenModelsOnceAndGatewayModelDiscovery(t *testing.T) {
	t.Parallel()
	in := input(t)
	in.Table = []models.Model{
		{Alias: "claude-opus-5-5"}, {Alias: "claude-sonnet-5-5"}, {Alias: "claude-haiku-5-5"},
		{Alias: "gemini-4-neon", Context: ptr(1_000_000)}, {Alias: "gpt-6-astra"},
		{Alias: "deepseek-v4.1-flash"},
	}
	in.Selection = harness.ModelSelection{
		Model:  "gemini-4-neon",
		Models: []string{"gemini-4-neon", "gpt-6-astra", "deepseek-v4.1-flash"},
	}
	setUp(t, in)
	in.Selection = harness.ModelSelection{}
	plan, err := claude.Launch(in)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plan.Env, discoveryVar+"=1") {
		t.Errorf("child env lacks %s=1: %q", discoveryVar, plan.Env)
	}
	if env, _ := decode(t, []byte(plan.Argv[2]))["env"].(map[string]any); env[discoveryVar] != "1" {
		t.Errorf("inline settings env = %v", env)
	}
	slotVars := []string{
		"ANTHROPIC_CUSTOM_MODEL_OPTION", "ANTHROPIC_DEFAULT_FABLE_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
	}
	for _, id := range []string{"gemini-4-neon[1m]", "gpt-6-astra", "deepseek-v4.1-flash"} {
		n := 0
		for _, v := range slotVars {
			if slices.Contains(plan.Env, v+"="+id) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s is in %d slots, want 1: %q", id, n, plan.Env)
		}
	}
}

func TestLaunchKeepsAChosenModelOverTierPinsInTheUsersOwnSettings(t *testing.T) {
	t.Parallel()
	in := input(t)
	in.Table = []models.Model{
		{Alias: "claude-opus-5-5"}, {Alias: "claude-sonnet-5-5"}, {Alias: "claude-haiku-5-5"},
		{Alias: "gemini-4-neon", Context: ptr(1_000_000)}, {Alias: "gpt-6-astra"},
	}
	in.Selection = harness.ModelSelection{
		Model:  "gpt-6-astra",
		Models: []string{"gpt-6-astra", "gemini-4-neon"},
	}
	write(t, filepath.Join(in.UserHome, ".claude", "settings.json"), `{"env":{
		"ANTHROPIC_DEFAULT_FABLE_MODEL":"claude-opus-5-5","ANTHROPIC_DEFAULT_OPUS_MODEL":"claude-opus-5-5",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":"claude-sonnet-5-5","ANTHROPIC_DEFAULT_HAIKU_MODEL":"claude-haiku-5-5",
		"ANTHROPIC_CUSTOM_MODEL_OPTION":"claude-haiku-5-5"}}`)
	setUp(t, in)
	in.Selection = harness.ModelSelection{}
	plan, err := claude.Launch(in)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plan.Env, "ANTHROPIC_MODEL=gpt-6-astra") {
		t.Errorf("chosen model did not reach the session: %q", plan.Env)
	}
	for _, id := range []string{"gpt-6-astra", "gemini-4-neon[1m]"} {
		n := 0
		for _, e := range plan.Env {
			if strings.HasSuffix(e, "="+id) && !strings.HasPrefix(e, "ANTHROPIC_MODEL=") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s is in %d slots, want 1: %q", id, n, plan.Env)
		}
	}
}

var mixTable = []models.Model{
	{Alias: "claude-opus-5-5"}, {Alias: "claude-sonnet-5-5"}, {Alias: "claude-haiku-5-5"},
	{Alias: "gemini-4-neon", Context: ptr(1_000_000)}, {Alias: "gpt-6-astra"},
	{Alias: "deepseek-v4.1-flash"},
}

var mixSelection = harness.ModelSelection{
	Model:  "gemini-4-neon",
	Models: []string{"gemini-4-neon", "gpt-6-astra", "deepseek-v4.1-flash"},
}

func fileEnv(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := decode(t, data)["env"].(map[string]any)
	return env
}

func TestKeepCurrentKeepsEveryGrantedSlotAndTheCustomOption(t *testing.T) {
	t.Parallel()
	in := input(t)
	in.Table = mixTable
	in.Selection = mixSelection
	setUp(t, in)
	path := filepath.Join(in.UserHome, claude.SettingsFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	in.Selection = harness.ModelSelection{}
	setUp(t, in)
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Errorf("Keep current changed the slots:\n%s\nwas:\n%s", after, before)
	}
}

func TestKeepCurrentLeavesABlankCustomOptionBlank(t *testing.T) {
	t.Parallel()
	in := input(t)
	in.Table = []models.Model{{Alias: "claude-sonnet-5-5"}}
	write(t, filepath.Join(in.UserHome, ".claude/settings.json"),
		`{"env":{"ANTHROPIC_CUSTOM_MODEL_OPTION":"retired-model"}}`)
	in.Selection = harness.ModelSelection{Preset: models.PresetAnthropic}
	setUp(t, in)
	in.Selection = harness.ModelSelection{}
	setUp(t, in)
	env := fileEnv(t, filepath.Join(in.UserHome, claude.SettingsFile))
	if v, ok := env["ANTHROPIC_CUSTOM_MODEL_OPTION"]; !ok || v != "" {
		t.Errorf("custom option = %v (present %v), want blank", v, ok)
	}
}

func TestKeepCurrentWithTheNativeDefaultCopiesTheMixToBothFiles(t *testing.T) {
	t.Parallel()
	in := input(t)
	in.Table = mixTable
	in.Selection = mixSelection
	setUp(t, in)
	in.Selection = harness.ModelSelection{}
	in.Default = true
	setUp(t, in)
	dedicated := filepath.Join(in.UserHome, claude.SettingsFile)
	native := filepath.Join(in.UserHome, ".claude", "settings.json")
	want, got := fileEnv(t, dedicated), fileEnv(t, native)
	for _, k := range []string{
		"ANTHROPIC_CUSTOM_MODEL_OPTION", "ANTHROPIC_DEFAULT_FABLE_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
	} {
		if got[k] != want[k] {
			t.Errorf("native %s = %v, dedicated %v", k, got[k], want[k])
		}
	}
	if m, _ := os.ReadFile(native); decode(t, m)["model"] != "gemini-4-neon[1m]" {
		t.Errorf("native model = %v", decode(t, m)["model"])
	}
}

func TestKeepCurrentWithTheNativeDefaultGivesTheNativeFileTheDedicatedSlotsAsASet(t *testing.T) {
	t.Parallel()
	in := input(t)
	in.Table = mixTable
	dedicated := filepath.Join(in.UserHome, claude.SettingsFile)
	native := filepath.Join(in.UserHome, ".claude", "settings.json")
	in.Default = true
	setUp(t, in) // an earlier native-default run: default Claude tier pins in both files
	in.Default = false
	in.Selection = mixSelection
	setUp(t, in)
	data, err := os.ReadFile(dedicated)
	if err != nil {
		t.Fatal(err)
	}
	cfg := decode(t, data)
	cfg["permissions"] = map[string]any{"allow": []any{"Bash"}}
	cfg["env"].(map[string]any)["UNRELATED_KEY"] = "x"
	out, _ := json.Marshal(cfg)
	write(t, dedicated, string(out))
	in.Selection = harness.ModelSelection{}
	in.Default = true
	setUp(t, in)
	want, got := fileEnv(t, dedicated), fileEnv(t, native)
	for _, k := range []string{
		"ANTHROPIC_CUSTOM_MODEL_OPTION", "ANTHROPIC_DEFAULT_FABLE_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
	} {
		if got[k] != want[k] {
			t.Errorf("native %s = %v, dedicated %v", k, got[k], want[k])
		}
	}
	if want["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "deepseek-v4.1-flash" {
		t.Fatalf("dedicated lost the mix: %v", want)
	}
	nativeData, _ := os.ReadFile(native)
	nativeCfg := decode(t, nativeData)
	if _, leaked := got["UNRELATED_KEY"]; leaked || nativeCfg["permissions"] != nil {
		t.Errorf("unrelated dedicated settings reached the native file: %s", nativeData)
	}
}

func TestKeepCurrentDropsSlotsPinnedToAModelTheKeyNoLongerGrants(t *testing.T) {
	t.Parallel()
	in := input(t)
	in.Table = mixTable
	path := filepath.Join(in.UserHome, claude.SettingsFile)
	write(t, path, `{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"retired-model",`+
		`"ANTHROPIC_DEFAULT_SONNET_MODEL":"gpt-6-astra","ANTHROPIC_CUSTOM_MODEL_OPTION":"retired-custom"}}`)
	setUp(t, in)
	env := fileEnv(t, path)
	if env["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "claude-opus-5-5" ||
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] != "gpt-6-astra" {
		t.Errorf("tier pins = %v", env)
	}
	if v, ok := env["ANTHROPIC_CUSTOM_MODEL_OPTION"]; ok {
		t.Errorf("custom option = %v, want dropped", v)
	}
}

func TestKeepCurrentWithTheNativeDefaultKeepsATierAliasAsTheNativeModel(t *testing.T) {
	t.Parallel()
	for model, want := range map[string]string{
		"opus": "opus", "sonnet[1m]": "sonnet[1m]", "retired-model": "gemini-4-neon[1m]",
	} {
		in := input(t)
		in.Table = mixTable
		in.Selection = mixSelection
		setUp(t, in)
		native := filepath.Join(in.UserHome, ".claude", "settings.json")
		write(t, native, `{"model":"`+model+`"}`)
		in.Selection = harness.ModelSelection{}
		in.Default = true
		setUp(t, in)
		data, _ := os.ReadFile(native)
		if got := decode(t, data)["model"]; got != want {
			t.Errorf("native model %q became %v, want %q", model, got, want)
		}
	}
}

func TestChoosingModelsDropsABlankCustomOptionKeepCurrentLeft(t *testing.T) {
	t.Parallel()
	in := input(t)
	in.Table = []models.Model{{Alias: "claude-sonnet-5-5"}}
	path := filepath.Join(in.UserHome, claude.SettingsFile)
	write(t, path, `{"env":{"ANTHROPIC_CUSTOM_MODEL_OPTION":""}}`)
	in.Selection = harness.ModelSelection{Preset: models.PresetAnthropic}
	setUp(t, in)
	env := fileEnv(t, path)
	if v, ok := env["ANTHROPIC_CUSTOM_MODEL_OPTION"]; ok {
		t.Errorf("custom option = %q, want dropped on a chosen selection", v)
	}
}
