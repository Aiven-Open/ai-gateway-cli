// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package omp_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/omp"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

const (
	baseURL = "https://gateway.example"
	key     = "sk-test-valid"
)

// granted holds one model per protocol, out of order.
func granted() []models.Model {
	return []models.Model{{Alias: "glm"}, {Alias: "gpt-sol", Name: "GPT Sol"}, {Alias: "claude-sonnet", Name: "Claude Sonnet"}}
}

func input(t *testing.T) harness.Input {
	t.Helper()
	return harness.Input{Table: granted(), BaseURL: baseURL, UserHome: t.TempDir()}
}

func agentPath(home, name string) string { return filepath.Join(home, ".omp", "agent", name) }

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
	if err := yaml.Unmarshal(f.Data, &v); err != nil {
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
	plan, err := omp.Setup(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range plan.Files {
		write(t, f.Path, string(f.Data))
	}
	return plan
}

func aivenOf(t *testing.T, f harness.File) map[string]any {
	t.Helper()
	providers, _ := decode(t, f)["providers"].(map[string]any)
	aiven, _ := providers["aiven"].(map[string]any)
	return aiven
}

func TestSetupAddsTheProviderWithEachModelOnItsProtocol(t *testing.T) {
	in := input(t)
	plan := setUp(t, in)
	if len(plan.Files) != 1 || plan.Files[0].Path != agentPath(in.UserHome, "models.yml") || !plan.Files[0].Backup ||
		!plan.Files[0].Models || plan.Argv != nil || plan.Env != nil {
		t.Fatalf("plan = %+v", plan)
	}
	aiven := aivenOf(t, plan.Files[0])
	entry := func(id, name, api, url string) any {
		return map[string]any{"id": id, "name": name, "api": api, "baseUrl": url}
	}
	want := []any{
		entry("claude-sonnet", "Claude Sonnet", "anthropic-messages", baseURL),
		entry("glm", "glm", "openai-completions", baseURL+"/v1"),
		entry("gpt-sol", "GPT Sol", "openai-responses", baseURL+"/v1"),
	}
	if aiven["baseUrl"] != baseURL+"/v1" || aiven["auth"] != "apiKey" || !strings.HasPrefix(aiven["apiKey"].(string), "!") ||
		!reflect.DeepEqual(aiven["models"], want) {
		t.Errorf("aiven = %v", aiven)
	}
}

// OMP runs an apiKey starting with ! as a command; the helper reads its own gateway's key file and nothing else.
func TestAPIKeyRunsTheKeyHelper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the helper is a POSIX shell command")
	}
	in := input(t)
	helper := strings.TrimPrefix(aivenOf(t, setUp(t, in).Files[0])["apiKey"].(string), "!")
	// Only another gateway's key file first: the helper must not read it.
	if _, err := creds.Write(in.UserHome, config.DefaultBaseURL, "sk-test-other", fsutil.WriteAtomic); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, want string
		ok         bool
	}{{"environment", "", false}, {"key file", key + "\n", true}} {
		if tc.name == "key file" {
			if _, err := creds.Write(in.UserHome, baseURL, key, fsutil.WriteAtomic); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("/bin/sh", "-c", helper)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + in.UserHome, creds.EnvVar + "=sk-test-env"}
		if out, err := cmd.Output(); (err == nil) != tc.ok || string(out) != tc.want {
			t.Errorf("%s: helper printed %q (%v), want %q", tc.name, out, err, tc.want)
		}
	}
}

func TestSetupKeepsTheUsersYAMLAndTheDefaultUnlessAsked(t *testing.T) {
	in := input(t)
	write(t, agentPath(in.UserHome, "models.yaml"), `# my providers
providers:
  local: # on this machine
    baseUrl: http://localhost
  aiven:
    models: [{id: old}]
    x: 1
other: true
`)
	write(t, agentPath(in.UserHome, "config.yaml"), "enabledModels: [local/*]\nmodelRoles:\n  default: local/qwen\n  smol: local/tiny\n")
	plan := setUp(t, in)
	if len(plan.Files) != 1 || plan.Files[0].Path != agentPath(in.UserHome, "models.yaml") {
		t.Fatalf("plan = %+v", plan)
	}
	got := decode(t, plan.Files[0])
	if got["other"] != true || !reflect.DeepEqual(got["providers"].(map[string]any)["local"], map[string]any{"baseUrl": "http://localhost"}) {
		t.Errorf("models.yaml = %v", got)
	}
	if aiven := aivenOf(t, plan.Files[0]); aiven["x"] != nil || len(aiven["models"].([]any)) != 3 {
		t.Errorf("aiven = %v", aiven)
	}
	for _, c := range []string{"# my providers", "# on this machine"} {
		if !strings.Contains(string(plan.Files[0].Data), c) {
			t.Errorf("comment %q lost:\n%s", c, plan.Files[0].Data)
		}
	}
	in.Default = true
	plan = setUp(t, in)
	if len(plan.Files) != 2 || plan.Files[1].Path != agentPath(in.UserHome, "config.yaml") || !plan.Files[1].Backup {
		t.Fatalf("plan = %+v", plan)
	}
	want := map[string]any{"enabledModels": []any{"local/*"}, "modelRoles": map[string]any{"default": "aiven/claude-sonnet", "smol": "local/tiny"}}
	if got := decode(t, plan.Files[1]); !reflect.DeepEqual(got, want) {
		t.Errorf("config.yaml = %v, want %v", got, want)
	}
}

func TestSetupStartsFromAnEmptyOrCommentOnlyFile(t *testing.T) {
	for name, data := range map[string]string{"empty": "", "comments only": "# nothing yet\n", "null": "~\n"} {
		t.Run(name, func(t *testing.T) {
			in := input(t)
			write(t, agentPath(in.UserHome, "models.yml"), data)
			plan := setUp(t, in)
			if aiven := aivenOf(t, plan.Files[0]); len(aiven["models"].([]any)) != 3 {
				t.Errorf("models.yml = %s", plan.Files[0].Data)
			}
			if name == "comments only" && !strings.Contains(string(plan.Files[0].Data), "# nothing yet") {
				t.Errorf("comment lost:\n%s", plan.Files[0].Data)
			}
		})
	}
}

func TestSetupRefusesFilesItCannotMerge(t *testing.T) {
	for _, tc := range []struct{ name, file, data, want string }{
		{"legacy models", "models.json", `{}`, "awaits native OMP migration"},
		{"legacy settings", "settings.json", `{}`, "awaits native OMP migration"},
		{"models malformed", "models.yml", "providers: [", "not a YAML object"},
		{"models a list", "models.yml", "- a\n", "not a YAML object"},
		{"providers not a mapping", "models.yml", "providers: [a]\n", "must be a direct YAML mapping"},
		{"two documents", "models.yml", "a: 1\n---\nb: 2\n", "one YAML mapping"},
		{"alias into the provider", "models.yml", "providers:\n  aiven: &p {x: 1}\n  copy: *p\n", "cannot preserve YAML"},
		{"config malformed", "config.yml", "modelRoles: [", "not a YAML object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input(t)
			write(t, agentPath(in.UserHome, tc.file), tc.data)
			if _, err := omp.Setup(in); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	in := input(t)
	in.UserHome = ""
	if _, err := omp.Setup(in); err == nil || !errors.Is(err, config.ErrNoHome) {
		t.Errorf("Setup without a home: %v", err)
	}
}

// A legacy file is OMP's to migrate; once the native file exists, it is ignored.
func TestSetupIgnoresALegacyFileBesideTheNativeOne(t *testing.T) {
	in := input(t)
	write(t, agentPath(in.UserHome, "models.json"), `{}`)
	write(t, agentPath(in.UserHome, "models.yml"), "{}\n")
	if _, err := omp.Setup(in); err != nil {
		t.Error(err)
	}
}

func TestLaunchAddsTheDefaultModelOnlyWithoutNativeArgs(t *testing.T) {
	in := input(t)
	if _, err := omp.Launch(in); err == nil {
		t.Error("Launch added a model setup did not configure")
	}
	setUp(t, in)
	for _, tc := range []struct{ args, want []string }{
		{nil, []string{"omp", "--model", "aiven/claude-sonnet"}},
		{[]string{"-p", "hi"}, []string{"omp", "-p", "hi"}},
		{[]string{"commit"}, []string{"omp", "commit"}},
		{[]string{"-m", "aiven/glm"}, []string{"omp", "-m", "aiven/glm"}},
		{[]string{"plugin", "list"}, []string{"omp", "plugin", "list"}},
	} {
		in.Args = tc.args
		plan, err := omp.Launch(in)
		if err != nil || plan.Files != nil || plan.Env != nil || !slices.Equal(plan.Argv, tc.want) {
			t.Errorf("Launch(%q) = %+v, %v; want argv %q", tc.args, plan, err, tc.want)
		}
	}
	in.Args = nil
	in.Table = []models.Model{{Alias: "claude-sonnet-5-5"}}
	if _, err := omp.Launch(in); err == nil || !strings.Contains(err.Error(), "refresh with setup") {
		t.Errorf("Launch with a default missing from the catalogue: %v", err)
	}
	write(t, agentPath(in.UserHome, "models.yml"), "providers: [")
	if _, err := omp.Launch(in); err == nil || strings.Contains(err.Error(), "refresh with setup") {
		t.Errorf("Launch with an unreadable models.yml: %v", err)
	}
}

func TestReadyChecksEachModelsProtocolAndTheKeySource(t *testing.T) {
	in := input(t)
	if _, err := omp.Ready(in.UserHome, baseURL); err == nil {
		t.Error("Ready before setup")
	}
	setUp(t, in)
	if needsEnv, err := omp.Ready(in.UserHome, baseURL); err != nil || needsEnv {
		t.Errorf("Ready after setup = %v, %v", needsEnv, err)
	}
	if _, err := omp.Ready(in.UserHome, "https://other.example"); err == nil {
		t.Error("Ready for another gateway")
	}
	wired := agentPath(in.UserHome, "models.yml")
	data, err := os.ReadFile(wired)
	if err != nil {
		t.Fatal(err)
	}
	write(t, wired, strings.ReplaceAll(string(data), creds.Rel(baseURL), creds.Rel(config.DefaultBaseURL)))
	if _, err := omp.Ready(in.UserHome, baseURL); err == nil {
		t.Error("Ready with another gateway's key helper")
	}
	write(t, wired, string(data))
	path := agentPath(in.UserHome, "models.yml")
	manual := "providers:\n  aiven:\n    apiKey: " + creds.EnvVar + "\n    auth: apiKey\n    models:\n" +
		"      - {id: glm, api: openai-completions, baseUrl: " + baseURL + "/v1}\n" +
		"      - {id: claude-sonnet, api: anthropic-messages, baseUrl: " + baseURL + "}\n"
	write(t, path, manual)
	if needsEnv, err := omp.Ready(in.UserHome, baseURL); err != nil || !needsEnv {
		t.Errorf("Ready with an environment key = %v, %v", needsEnv, err)
	}
	for name, data := range map[string]string{
		"model on the wrong protocol": strings.Replace(manual, "api: anthropic-messages", "api: openai-completions", 1),
		"model at another base":       strings.Replace(manual, "baseUrl: "+baseURL+"}", "baseUrl: "+baseURL+"/v1}", 1),
		"model without an id":         strings.Replace(manual, "{id: glm, ", "{", 1),
		"another key source":          strings.Replace(manual, creds.EnvVar, "sk-literal", 1),
		"another auth":                strings.Replace(manual, "auth: apiKey", "auth: oauth", 1),
		"no models":                   "providers:\n  aiven: {apiKey: " + creds.EnvVar + ", auth: apiKey, models: []}\n",
	} {
		write(t, path, data)
		if _, err := omp.Ready(in.UserHome, baseURL); err == nil {
			t.Errorf("Ready with %s", name)
		}
	}
	write(t, path, manual)
	write(t, agentPath(in.UserHome, "config.yml"), "modelRoles: [")
	if _, err := omp.Ready(in.UserHome, baseURL); err == nil {
		t.Error("Ready with an unreadable config.yml")
	}
}

// Without a home, the relative paths would name files in the working directory.
func TestReadyAndLaunchRefuseAMissingHome(t *testing.T) {
	in := input(t)
	setUp(t, in)
	t.Chdir(in.UserHome)
	if _, err := omp.Ready("", baseURL); err == nil || !errors.Is(err, config.ErrNoHome) {
		t.Errorf("Ready without a home: %v", err)
	}
	in.UserHome = ""
	if _, err := omp.Launch(in); err == nil || !errors.Is(err, config.ErrNoHome) {
		t.Errorf("Launch without a home: %v", err)
	}
}
