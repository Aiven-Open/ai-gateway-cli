// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package pi_test

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
	"github.com/aiven-open/ai-gateway-cli/internal/harness/pi"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

const (
	baseURL = "https://gateway.example"
	key     = "sk-test-valid"
)

func ptr[T any](v T) *T { return &v }

// granted holds one model per protocol: Messages with a window and an output limit, Responses priced, and a Chat
// Completions model with neither.
func granted() []models.Model {
	return []models.Model{
		{Alias: "glm"},
		{Alias: "gpt-sol", Name: "GPT Sol", InUSD1M: ptr(1.25), OutUSD1M: ptr(10.0)},
		{Alias: "claude-sonnet", Name: "Claude Sonnet", Context: ptr(1_000_000), MaxOutput: ptr(64_000)},
	}
}

func input(t *testing.T) harness.Input {
	t.Helper()
	return harness.Input{Table: granted(), BaseURL: baseURL, UserHome: t.TempDir()}
}

func agentPath(in harness.Input, name string) string {
	return filepath.Join(in.UserHome, ".pi", "agent", name)
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

func decode(t *testing.T, f harness.File) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(f.Data, &v); err != nil {
		t.Fatalf("%s: %v", f.Path, err)
	}
	if strings.Contains(string(f.Data), key) {
		t.Errorf("%s holds the key:\n%s", f.Path, f.Data)
	}
	return v
}

// setUp runs Setup and writes its files, as setup does after its preview.
func setUp(t *testing.T, in harness.Input) harness.Plan {
	t.Helper()
	plan, err := pi.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range plan.Files {
		write(t, f.Path, string(f.Data))
	}
	return plan
}

func TestSetupAddsTheProviderWithEachModelOnItsProtocol(t *testing.T) {
	in := input(t)
	plan := setUp(t, in)
	if len(plan.Files) != 1 || plan.Files[0].Path != agentPath(in, "models.json") || !plan.Files[0].Backup || !plan.Files[0].Models ||
		plan.Argv != nil || plan.Env != nil {
		t.Fatalf("plan = %+v", plan)
	}
	entry := func(id, name, api, url string) map[string]any {
		return map[string]any{"id": id, "name": name, "api": api, "baseUrl": url, "reasoning": true, "input": []any{"text", "image"}}
	}
	sonnet := entry("claude-sonnet", "Claude Sonnet", "anthropic-messages", baseURL)
	sonnet["contextWindow"], sonnet["maxTokens"] = 1e6, 64000.0
	glm := entry("glm", "glm", "openai-completions", baseURL+"/v1")
	sol := entry("gpt-sol", "GPT Sol", "openai-responses", baseURL+"/v1")
	sol["cost"] = map[string]any{"input": 1.25, "output": 10.0, "cacheRead": 0.0, "cacheWrite": 0.0}
	want := map[string]any{"providers": map[string]any{"aiven": map[string]any{
		"baseUrl": baseURL + "/v1", "api": "openai-completions", "apiKey": pi.APIKey(baseURL), "models": []any{sonnet, glm, sol},
	}}}
	if got := decode(t, plan.Files[0]); !reflect.DeepEqual(got, want) {
		t.Errorf("models.json = %v\nwant %v", got, want)
	}
}

func TestSetupKeepsOtherProvidersAndTheDefaultUnlessAsked(t *testing.T) {
	in := input(t)
	write(t, agentPath(in, "models.json"), `{"providers":{"local":{"baseUrl":"http://localhost"},"aiven":{"models":[{"id":"old"}],"x":1}},"other":true}`)
	write(t, agentPath(in, "settings.json"), `{"defaultProvider":"local","theme":"dark"}`)
	got := decode(t, setUp(t, in).Files[0])
	providers := got["providers"].(map[string]any)
	aiven := providers["aiven"].(map[string]any)
	if got["other"] != true || !reflect.DeepEqual(providers["local"], map[string]any{"baseUrl": "http://localhost"}) ||
		aiven["x"] != nil || len(aiven["models"].([]any)) != 3 {
		t.Errorf("models.json = %v", got)
	}
	in.Default = true
	plan := setUp(t, in)
	if len(plan.Files) != 2 || plan.Files[1].Path != agentPath(in, "settings.json") || !plan.Files[1].Backup {
		t.Fatalf("plan = %+v", plan)
	}
	want := map[string]any{"defaultProvider": "aiven", "defaultModel": "claude-sonnet", "theme": "dark"}
	if got := decode(t, plan.Files[1]); !reflect.DeepEqual(got, want) {
		t.Errorf("settings.json = %v, want %v", got, want)
	}
}

// Pi reads // comments in models.json, so they stay; it reads settings.json as plain JSON, so its comments and
// trailing commas go and the layout stays.
func TestSetupEditsBothFilesInPlace(t *testing.T) {
	in := input(t)
	in.Default = true
	local := "  // the user's own provider\n  \"local\": {\"baseUrl\": \"http://localhost\"}"
	write(t, agentPath(in, "models.json"), "{\n/* gone */\n\"providers\": {\n"+local+"\n}\n}\n")
	write(t, agentPath(in, "settings.json"), "{\n  // gone\n  \"theme\": \"dark\", /* gone */\n}\n")
	plan := setUp(t, in)
	cfg, err := harness.ReadObject(agentPath(in, "models.json"))
	aiven, _ := cfg["providers"].(map[string]any)["aiven"].(map[string]any)
	if got := string(plan.Files[0].Data); err != nil || !strings.HasPrefix(got, "{\n\"providers\": {\n"+local+",\n") ||
		aiven["apiKey"] != pi.APIKey(baseURL) {
		t.Errorf("models.json (%v) =\n%s", err, got)
	}
	if got := string(plan.Files[1].Data); !strings.HasPrefix(got, "{\n  \"theme\": \"dark\",\n") ||
		!strings.Contains(got, `"defaultModel": "claude-sonnet"`) || !json.Valid(plan.Files[1].Data) {
		t.Errorf("settings.json =\n%s", got)
	}
}

func TestSetupRefusesFilesItCannotMerge(t *testing.T) {
	for _, tc := range []struct{ name, file, data string }{
		{"models malformed", "models.json", `{"providers":`},
		{"providers not an object", "models.json", `{"providers":[]}`},
		{"settings malformed", "settings.json", `[`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input(t)
			write(t, agentPath(in, tc.file), tc.data)
			if _, err := pi.Setup(in); err == nil {
				t.Fatal("Setup accepted an unreadable file")
			}
		})
	}
	in := input(t)
	in.UserHome = ""
	if _, err := pi.Setup(in); err == nil || !errors.Is(err, config.ErrNoHome) {
		t.Errorf("Setup without a home: %v", err)
	}
}

// Pi sends a credential saved for aiven in auth.json instead of running the key command, so the key setup checks
// would not be the one sent.
func TestSetupRefusesACredentialPiSavedForAiven(t *testing.T) {
	for _, tc := range []struct {
		name, auth string
		refused    bool
	}{
		{"no file", "", false},
		{"other providers", `{"anthropic":{"type":"api_key","key":"sk-ant-other"}}`, false},
		{"aiven key", `{"aiven":{"type":"api_key","key":"sk-saved"}}`, true},
		{"aiven oauth", `{"aiven":{"type":"oauth"}}`, true},
		{"malformed", `{"aiven":`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input(t)
			if tc.auth != "" {
				write(t, agentPath(in, "auth.json"), tc.auth)
			}
			_, err := pi.Setup(in)
			if (err != nil) != tc.refused {
				t.Fatalf("Setup: %v, want refused %v", err, tc.refused)
			}
			if err != nil && strings.Contains(err.Error(), "sk-saved") {
				t.Errorf("error shows the credential: %v", err)
			}
		})
	}
}

// pi runs an apiKey starting with ! through /bin/sh. The helper reads its own gateway's key file and nothing else.
func TestAPIKeyRunsTheKeyHelper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the helper is a POSIX shell command")
	}
	home := t.TempDir()
	// Only another gateway's key file first: the helper must not read it.
	if _, err := creds.Write(home, config.DefaultBaseURL, "sk-test-other", fsutil.WriteAtomic); err != nil {
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
		cmd := exec.Command("/bin/sh", "-c", strings.TrimPrefix(pi.APIKey(baseURL), "!"))
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, creds.EnvVar + "=sk-test-env"}
		if out, err := cmd.Output(); (err == nil) != tc.ok || string(out) != tc.want {
			t.Errorf("%s: helper printed %q (%v), want %q", tc.name, out, err, tc.want)
		}
	}
}

func TestLaunchAddsTheDefaultModelOnlyWithoutNativeArgs(t *testing.T) {
	in := input(t)
	if _, err := pi.Launch(in); err == nil {
		t.Error("Launch added a model setup did not configure")
	}
	setUp(t, in)
	for _, tc := range []struct{ args, want []string }{
		{nil, []string{"pi", "--model", "aiven/claude-sonnet"}},
		{[]string{"-p", "hi"}, []string{"pi", "-p", "hi"}},
		{[]string{"--model", "aiven/glm"}, []string{"pi", "--model", "aiven/glm"}},
		{[]string{"--provider", "openai"}, []string{"pi", "--provider", "openai"}},
		{[]string{"install", "x"}, []string{"pi", "install", "x"}},
	} {
		in.Args = tc.args
		plan, err := pi.Launch(in)
		if err != nil || plan.Files != nil || plan.Env != nil || !slices.Equal(plan.Argv, tc.want) {
			t.Errorf("Launch(%q) = %+v, %v; want argv %q", tc.args, plan, err, tc.want)
		}
	}
	in.Table = []models.Model{{Alias: "claude-sonnet-5-5"}}
	in.Args = nil
	if _, err := pi.Launch(in); err == nil || !strings.Contains(err.Error(), "refresh with setup") {
		t.Errorf("Launch with a default missing from the catalogue: %v", err)
	}
	write(t, agentPath(in, "models.json"), `{`)
	if _, err := pi.Launch(in); err == nil || strings.Contains(err.Error(), "refresh with setup") {
		t.Errorf("Launch with an unreadable models.json: %v", err)
	}
}

func TestReadyChecksEachModelsProtocolAndTheKeySource(t *testing.T) {
	in := input(t)
	if _, err := pi.Ready(in.UserHome, baseURL); err == nil {
		t.Error("Ready before setup")
	}
	setUp(t, in)
	if needsEnv, err := pi.Ready(in.UserHome, baseURL); err != nil || needsEnv {
		t.Errorf("Ready after setup = %v, %v", needsEnv, err)
	}
	if _, err := pi.Ready(in.UserHome, "https://other.example"); err == nil {
		t.Error("Ready for another gateway")
	}
	wired := agentPath(in, "models.json")
	data, err := os.ReadFile(wired)
	if err != nil {
		t.Fatal(err)
	}
	write(t, wired, strings.ReplaceAll(string(data), creds.Rel(baseURL), creds.Rel(config.DefaultBaseURL)))
	if _, err := pi.Ready(in.UserHome, baseURL); err == nil {
		t.Error("Ready with another gateway's key helper")
	}
	write(t, wired, string(data))
	path := agentPath(in, "models.json")
	manual := `{"providers":{"aiven":{"baseUrl":"` + baseURL + `/v1","api":"openai-completions","apiKey":"$` + creds.EnvVar +
		`","models":[{"id":"glm"},{"id":"claude-sonnet","api":"anthropic-messages","baseUrl":"` + baseURL + `"}]}}}`
	for _, value := range []string{"$" + creds.EnvVar, "${" + creds.EnvVar + "}"} {
		write(t, path, strings.Replace(manual, "$"+creds.EnvVar, value, 1))
		if needsEnv, err := pi.Ready(in.UserHome, baseURL); err != nil || !needsEnv {
			t.Errorf("Ready with environment reference %q = %v, %v", value, needsEnv, err)
		}
	}
	for name, data := range map[string]string{
		"model on the wrong protocol": strings.Replace(manual, `"api":"anthropic-messages"`, `"api":"openai-completions"`, 1),
		"another key source":          strings.Replace(manual, "$"+creds.EnvVar, "sk-literal", 1),
		"literal environment name":    strings.Replace(manual, "$"+creds.EnvVar, creds.EnvVar, 1),
		"no models":                   `{"providers":{"aiven":{"apiKey":"$` + creds.EnvVar + `","models":[]}}}`,
	} {
		write(t, path, data)
		if _, err := pi.Ready(in.UserHome, baseURL); err == nil {
			t.Errorf("Ready with %s", name)
		}
	}
	write(t, path, manual)
	write(t, agentPath(in, "settings.json"), `{`)
	if _, err := pi.Ready(in.UserHome, baseURL); err == nil {
		t.Error("Ready with unreadable settings")
	}
}

// Without a home, the relative paths would name files in the working directory.
func TestReadyAndLaunchRefuseAMissingHome(t *testing.T) {
	in := input(t)
	setUp(t, in)
	t.Chdir(in.UserHome)
	if _, err := pi.Ready("", baseURL); err == nil || !errors.Is(err, config.ErrNoHome) {
		t.Errorf("Ready without a home: %v", err)
	}
	in.UserHome = ""
	if _, err := pi.Launch(in); err == nil || !errors.Is(err, config.ErrNoHome) {
		t.Errorf("Launch without a home: %v", err)
	}
}

// WindowsShell finds bash as Pi does on Windows and refuses what would not run the helper: no bash, or the legacy WSL
// launcher, which Pi feeds the command to inside Linux.
func TestWindowsShellFindsTheBashPiRunsTheHelperIn(t *testing.T) {
	const (
		gitBash = `C:\Program Files\Git\bin\bash.exe`
		x86Bash = `C:\Program Files (x86)\Git\bin\bash.exe`
		msys    = `C:\msys64\usr\bin\bash.exe`
	)
	environ := []string{`programfiles=C:\Program Files`, `ProgramFiles(x86)=C:\Program Files (x86)`, `SystemRoot=C:\Windows`}
	for name, tc := range map[string]struct {
		environ []string
		files   []string
		onPath  string
		want    string
	}{
		"Git Bash":                {environ, []string{gitBash, x86Bash}, msys, gitBash},
		"32-bit Git Bash":         {environ, []string{x86Bash}, msys, x86Bash},
		"bash on PATH":            {environ, nil, msys, msys},
		"none":                    {environ, nil, "", ""},
		"WSL launcher":            {environ, nil, `c:\windows\SYSTEM32\bash.exe`, ""},
		"WSL launcher, Sysnative": {environ, nil, `C:/Windows/Sysnative/bash.exe`, ""},
		"WSL launcher, no root":   {nil, nil, `C:\Windows\System32\bash.exe`, ""},
		"bash elsewhere, no root": {nil, nil, msys, msys},
	} {
		t.Run(name, func(t *testing.T) {
			exists := func(path string) bool { return slices.Contains(tc.files, path) }
			lookPath := func(file string) (string, error) {
				if file != "bash.exe" || tc.onPath == "" {
					return "", errors.New("not found")
				}
				return tc.onPath, nil
			}
			got, err := pi.WindowsShell(tc.environ, exists, lookPath)
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Errorf("WindowsShell = %q, %v; want %q", got, err, tc.want)
			}
			if err != nil && !strings.Contains(err.Error(), "install Git for Windows") {
				t.Errorf("error %q does not say how to fix it", err)
			}
		})
	}
}
