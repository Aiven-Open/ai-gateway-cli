// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

// productNames are the harness names the launcher prints in headings, prompts and summaries.
var productNames = map[string]string{"claude": "Claude Code", "codex": "Codex", "pi": "Pi", "opencode": "OpenCode", "omp": "OMP"}

func configurationSnapshot(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if entry.IsDir() {
				out[path] = info.Mode().String()
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[path] = fmt.Sprintf("%s:%x", info.Mode(), sha256.Sum256(data))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// Launch announces the model listing it sends and the start, and nothing else: no inference stage, no model table,
// no second request.
func TestLaunchAnnouncesOnlyTheCatalogueAndTheStart(t *testing.T) {
	for _, tool := range []string{"claude", "codex", "pi", "opencode", "omp"} {
		t.Run(tool, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g).loggedIn(keyValid).configured(tool)
			before := len(g.journal())
			r := s.run("launch", tool)
			if r.code != 0 || r.stdout != "" || r.stderr != launched(s.env.BaseURL, 5, productNames[tool]) {
				t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
			reqs := g.journal()[before:]
			if len(reqs) != 1 || reqs[0].Method != http.MethodGet || reqs[0].URL.Path != "/v1/models" {
				t.Errorf("launch sent %d requests, want one model listing", len(reqs))
			}
			if len(s.runner.calls) != 1 || s.runner.calls[0].name != tool {
				t.Errorf("exec %v", s.runner.calls)
			}
		})
	}
}

func TestLaunchPlainFlagIsAcceptedAndNotForwarded(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).configured("pi")
	r := s.run("launch", "--plain", "pi", "--", "--plain")
	if r.code != 0 || len(s.runner.calls) != 1 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	argv := s.runner.calls[0].argv
	if argv[len(argv)-1] != "--plain" || len(argv) > 1 && argv[len(argv)-2] == "--plain" {
		t.Errorf("argv %q, want only the native --plain forwarded", argv)
	}
}

func TestLaunchRequiresNativeSetupBeforeCredentialOrNetworkAccess(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"claude", "codex", "pi", "opencode", "omp"} {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			g := newGatewayDouble(t)
			s := newSession(t, g)
			before := configurationSnapshot(t, s.fakeHome, s.env.WorkDir)
			r := s.run("launch", tool)
			if r.code != 1 || !strings.Contains(r.stderr, "Run: aiven-ai-gateway setup "+tool) {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
			if len(g.journal()) != 0 || len(s.runner.calls) != 0 {
				t.Fatal("missing setup reached the gateway or a harness")
			}
			if after := configurationSnapshot(t, s.fakeHome, s.env.WorkDir); !reflect.DeepEqual(before, after) {
				t.Fatal("missing setup changed the configuration tree")
			}
		})
	}
}

func TestLaunchLeavesCompleteConfigurationTreeAndWriterUntouched(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"claude", "codex", "pi", "opencode", "omp"} {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).configured(tool)
			before := configurationSnapshot(t, s.fakeHome, s.env.WorkDir)
			s.deps.WriteFile = func(string, []byte, os.FileMode) error {
				t.Error("launch called the persistent writer")
				return os.ErrPermission
			}
			native := []string{"--model", "native-choice", "two words", "", "$(never execute)"}
			r := s.run(append([]string{"launch", tool, "--"}, native...)...)
			if r.code != 0 || len(s.runner.calls) != 1 {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
			got := s.runner.calls[0].argv
			if !reflect.DeepEqual(got[len(got)-len(native):], native) {
				t.Fatalf("native argv boundaries changed: %q", got)
			}
			if after := configurationSnapshot(t, s.fakeHome, s.env.WorkDir); !reflect.DeepEqual(before, after) {
				t.Fatal("launch changed the configuration tree")
			}
		})
	}
}

func TestLaunchRejectsMissingConfiguredDefaultWithoutRefreshingMetadata(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"pi", "opencode"} {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			s := newSession(t, newGatewayDouble(t)).configured(tool).loggedIn(keyMidContext)
			before := configurationSnapshot(t, s.fakeHome)
			r := s.run("launch", tool)
			if r.code != 1 || !strings.Contains(r.stderr, "selected default is absent") || len(s.runner.calls) != 0 {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
			if after := configurationSnapshot(t, s.fakeHome); !reflect.DeepEqual(before, after) {
				t.Fatal("stale configuration was silently refreshed")
			}
		})
	}
}

func TestLaunchAcceptsCompatibleManualEnvironmentProvider(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"pi", "codex", "omp"} {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).configured(tool)
			switch tool {
			case "pi":
				path := filepath.Join(s.fakeHome, toolFiles["pi"])
				cfg := readJSON(t, path)
				provider := cfg["providers"].(map[string]any)["aiven"].(map[string]any)
				provider["apiKey"] = "$AIVEN_AI_GATEWAY_KEY"
				data, err := json.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, path, string(data))
			case "codex":
				writeFile(t, filepath.Join(s.fakeHome, ".codex/config.toml"),
					"[model_providers.aiven]\nbase_url = '"+s.env.BaseURL+"/v1'\nwire_api = 'responses'\nenv_key = 'AIVEN_AI_GATEWAY_KEY'\n")
				writeFile(t, filepath.Join(s.fakeHome, ".codex/aiven.config.toml"),
					"model = 'gpt-sol'\nmodel_provider = 'aiven'\n")
			case "omp":
				writeFile(t, filepath.Join(s.fakeHome, ".omp/agent/models.yml"),
					"providers:\n  aiven:\n    apiKey: AIVEN_AI_GATEWAY_KEY\n    auth: apiKey\n    models:\n"+
						"      - {id: claude-sonnet, api: anthropic-messages, baseUrl: '"+s.env.BaseURL+"'}\n")
			}
			s.env.Environ = append(s.env.Environ, "AIVEN_AI_GATEWAY_KEY=synthetic-other-key", "EDITOR=synthetic-editor")
			if runtime.GOOS == "windows" {
				s.env.Environ = append(s.env.Environ, "aiven_ai_gateway_key=synthetic-lower-case-key")
			}
			before := configurationSnapshot(t, s.fakeHome)
			r := s.run("launch", tool)
			if r.code != 0 || len(s.runner.calls) != 1 {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
			child := s.runner.calls[0]
			if key, ok := child.envVar("AIVEN_AI_GATEWAY_KEY"); !ok || key != keyValid {
				t.Fatal("manual environment reference did not receive the validated saved key")
			}
			count := 0
			for _, kv := range child.env {
				name, _, _ := strings.Cut(kv, "=")
				if config.SameVar(name, "AIVEN_AI_GATEWAY_KEY") {
					count++
				}
			}
			if editor, _ := child.envVar("EDITOR"); count != 1 || editor != "synthetic-editor" {
				t.Errorf("gateway entries %d, editor %q", count, editor)
			}
			if after := configurationSnapshot(t, s.fakeHome); !reflect.DeepEqual(before, after) {
				t.Fatal("manual configuration was rewritten")
			}
		})
	}
}

func TestFileBackedLaunchClearsInheritedGatewayKey(t *testing.T) {
	for _, tool := range []string{"claude", "codex", "pi", "opencode", "omp"} {
		t.Run(tool, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).configured(tool)
			s.env.Environ = append(s.env.Environ,
				"AIVEN_AI_GATEWAY_KEY=sk-inherited-other",
				"ANTHROPIC_AUTH_TOKEN=sk-inherited-anthropic",
				"EDITOR=synthetic-editor",
			)
			if runtime.GOOS == "windows" {
				s.env.Environ = append(s.env.Environ, "aiven_ai_gateway_key=sk-inherited-lower")
			}
			r := s.run("launch", tool)
			if r.code != 0 {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
			c := s.launched()
			for _, kv := range c.env {
				name, _, _ := strings.Cut(kv, "=")
				if config.SameVar(name, "AIVEN_AI_GATEWAY_KEY") {
					t.Errorf("gateway credential variable reached child: %q", name)
				}
			}
			if value, _ := c.envVar("EDITOR"); value != "synthetic-editor" {
				t.Errorf("unrelated environment changed: %q", value)
			}
			if tool == "claude" {
				if value, exists := c.envVar("ANTHROPIC_AUTH_TOKEN"); !exists || value != "" {
					t.Errorf("Claude auth token = %q (set %v), want cleared", value, exists)
				}
			}
		})
	}
}

func TestLaunchNeverSelectsAnotherGatewaysKey(t *testing.T) {
	for _, tool := range []string{"claude", "codex", "pi", "opencode", "omp"} {
		t.Run(tool, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g).configured(tool)
			if _, err := creds.Write(s.fakeHome, "https://other.example:8443", keyValid, fsutil.WriteAtomic); err != nil {
				t.Fatal(err)
			}
			s.env.Environ = append(s.env.Environ, creds.EnvVar+"="+keyValid)
			beforeRequests := len(g.journal())
			before := configurationSnapshot(t, s.fakeHome)
			r := s.run("launch", tool)
			if r.code != 1 || len(g.journal()) != beforeRequests || len(s.runner.calls) != 0 {
				t.Fatalf("exit %d, gateway requests %d, launches %d: %s", r.code, len(g.journal())-beforeRequests, len(s.runner.calls), r.stderr)
			}
			if after := configurationSnapshot(t, s.fakeHome); !reflect.DeepEqual(before, after) {
				t.Fatal("launch changed the other gateway's key or configuration")
			}
		})
	}
}

func TestPiLaunchRejectsModelOriginOverrideBeforeCredentialsOrNetwork(t *testing.T) {
	t.Parallel()
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid).configured("pi")
	path := filepath.Join(s.fakeHome, toolFiles["pi"])
	cfg := readJSON(t, path)
	provider := cfg["providers"].(map[string]any)["aiven"].(map[string]any)
	list := provider["models"].([]any)
	list[0].(map[string]any)["baseUrl"] = "https://other.invalid/v1"
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(data))
	before := len(g.journal())
	r := s.run("launch", "pi")
	if r.code != 1 || !strings.Contains(r.stderr, "incompatible Aiven model wiring") ||
		len(g.journal()) != before || len(s.runner.calls) != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
}

func TestCodexLaunchRejectsConflictingCredentialReferenceBeforeNetwork(t *testing.T) {
	t.Parallel()
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid).configured("codex")
	path := filepath.Join(s.fakeHome, ".codex/aiven.config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("[model_providers.aiven]\n"),
		[]byte("[model_providers.aiven]\nenv_key = 'OTHER_SECRET_REFERENCE'\n"), 1)
	writeFile(t, path, string(data))
	before := len(g.journal())
	r := s.run("launch", "codex")
	if r.code != 1 || !strings.Contains(r.stderr, "incompatible Aiven provider wiring") ||
		len(g.journal()) != before || len(s.runner.calls) != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
}

func TestLaunchAcceptsConsoleProviderInheritance(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"pi", "codex"} {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).configured(tool)
			if tool == "codex" {
				writeFile(t, filepath.Join(s.fakeHome, ".codex/aiven.config.toml"),
					"model = 'gpt-sol'\nmodel_provider = 'aiven'\n")
			} else {
				path := filepath.Join(s.fakeHome, toolFiles["pi"])
				cfg := readJSON(t, path)
				provider := cfg["providers"].(map[string]any)["aiven"].(map[string]any)
				provider["baseUrl"], provider["api"] = s.env.BaseURL, "anthropic-messages"
				provider["models"] = []any{map[string]any{"id": "claude-sonnet", "name": "claude-sonnet"}}
				data, err := json.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, path, string(data))
			}
			before := configurationSnapshot(t, s.fakeHome)
			r := s.run("launch", tool)
			if r.code != 0 || len(s.runner.calls) != 1 {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
			if after := configurationSnapshot(t, s.fakeHome); !reflect.DeepEqual(before, after) {
				t.Fatal("Console configuration was rewritten")
			}
		})
	}
}

func TestClaudeLaunchAcceptsCanonicalManualSetupAndProjectModelField(t *testing.T) {
	t.Parallel()
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).configured("claude")
	dedicated := filepath.Join(s.fakeHome, toolFiles["claude"])
	canonical := filepath.Join(s.fakeHome, ".claude", "settings.json")
	if err := os.Rename(dedicated, canonical); err != nil {
		t.Fatal(err)
	}
	s.writeProjectSettings("settings.json", `{"model":"claude-opus","env":{"PROJECT_EXTRA":"retained"}}`)
	before := configurationSnapshot(t, s.fakeHome, s.env.WorkDir)
	r := s.run("launch", "claude")
	if r.code != 0 || len(s.runner.calls) != 1 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if settings := launchSettings(t, s.runner.calls[0]); settings["model"] != "claude-opus[1m]" {
		t.Fatalf("project model field lost: %v", settings["model"])
	}
	if after := configurationSnapshot(t, s.fakeHome, s.env.WorkDir); !reflect.DeepEqual(before, after) {
		t.Fatal("manual configuration was rewritten")
	}
}

func TestLaunchFailsBeforeGatewayOrWritesWhenBinaryMissing(t *testing.T) {
	for _, h := range []string{"claude", "codex", "pi", "opencode", "omp"} {
		t.Run(h, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g).loggedIn(keyValid).configured(h)
			before := configurationSnapshot(t, s.fakeHome)
			s.runner.missing = []string{h}
			r := s.run("launch", h)
			if want := "aiven-ai-gateway: " + h + " is required but not installed; install it, then run aiven-ai-gateway setup " + h + "\n"; r.code != 1 || r.stdout != "" || r.stderr != want {
				t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
			if n := len(g.journal()); n != 1 {
				t.Errorf("launch sent %d requests", n-1)
			}
			if after := configurationSnapshot(t, s.fakeHome); !reflect.DeepEqual(before, after) || len(s.runner.calls) != 0 {
				t.Errorf("wrote files or exec'd %v", s.runner.calls)
			}
		})
	}
}

func TestLaunchExecsNothingWhenTheKeyCannotListModels(t *testing.T) {
	cases := []struct {
		name, key, want string
	}{
		{"no key", "", "aiven-ai-gateway: no key stored. Run: aiven-ai-gateway login\n"},
		{"gateway rejects key", "sk-bad", catalogueFailed("the gateway rejected the key", "key rejected by gateway (401): Authentication Error. Create a key in Aiven Console and run: aiven-ai-gateway login")},
		{"key grants no models", keyEmpty, catalogueFailed("the key grants no models", "key grants no models. Create a key in Aiven Console and run: aiven-ai-gateway login")},
	}
	for _, h := range []string{"claude", "codex", "pi", "opencode", "omp"} {
		for _, tc := range cases {
			t.Run(h+"/"+tc.name, func(t *testing.T) {
				s := newSession(t, newGatewayDouble(t)).configured(h)
				if tc.key != "" {
					s.loggedIn(tc.key)
				}
				r := s.run("launch", h)
				want := tc.want
				if h == "opencode" && tc.key == "" {
					want = "aiven-ai-gateway: OpenCode requires a key saved for this gateway. Run: aiven-ai-gateway login\n"
				}
				want = "Gateway: " + s.env.BaseURL + "\n" + want
				if r.code != 1 || r.stdout != "" || r.stderr != want {
					t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
				}
				if len(s.runner.calls) != 0 {
					t.Errorf("exec'd %v", s.runner.calls)
				}
			})
		}
	}
}

// A variable that moves a tool's config away from the file setup writes refuses the launch, as it refuses setup.
func TestLaunchRefusesAToolWhoseConfigIsMoved(t *testing.T) {
	for _, tc := range []struct{ h, name string }{
		{"pi", "PI_CODING_AGENT_DIR"},
		{"opencode", "OPENCODE_CONFIG"},
		{"opencode", "OPENCODE_CONFIG_DIR"},
		{"codex", "CODEX_HOME"},
		{"claude", "CLAUDE_CONFIG_DIR"},
		{"omp", "PI_CODING_AGENT_DIR"},
		{"omp", "OMP_PROFILE"},
		{"omp", "PI_PROFILE"},
		{"omp", "PI_CONFIG_DIR"},
	} {
		h, name := tc.h, tc.name
		t.Run(h+"/"+name, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g).loggedIn(keyValid).configured(h)
			before := len(g.journal())
			s.env.Environ = append(s.env.Environ, name+"="+filepath.Join(s.fakeHome, "elsewhere"))
			r := s.run("launch", h)
			want := "aiven-ai-gateway: " + name + " is set, so " + h + " would not read the config aiven-ai-gateway writes; unset it and rerun, or configure " + h + " by hand\n"
			if r.code != 1 || r.stderr != want || len(s.runner.calls) != 0 || len(g.journal()) != before {
				t.Fatalf("exit %d, stderr %q, exec'd %v, %d gateway requests", r.code, r.stderr, s.runner.calls, len(g.journal())-before)
			}
			s.env.Environ[len(s.env.Environ)-1] = name + "="
			if r := s.run("launch", h); r.code != 0 {
				t.Errorf("empty %s: exit %d, stderr %q", name, r.code, r.stderr)
			}
		})
	}
}

// The adapters that read the key file themselves get the key nowhere else.
func TestLaunchKeepsKeyOutOfArgvEnvAndConfig(t *testing.T) {
	for _, h := range []string{"codex", "pi", "opencode", "omp"} {
		t.Run(h, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).configured(h)
			s.run("launch", h, "--", "--flag")
			c := s.launched()
			for _, a := range c.argv {
				if strings.Contains(a, keyValid) {
					t.Errorf("key in argv %q", c.argv)
				}
			}
			for _, kv := range c.env {
				if strings.Contains(kv, keyValid) {
					t.Errorf("key in env entry %q", kv)
				}
			}
			_ = filepath.WalkDir(s.fakeHome, func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && p != s.keyPath() {
					if data, _ := os.ReadFile(p); strings.Contains(string(data), keyValid) {
						t.Errorf("key written to %s", p)
					}
				}
				return nil
			})
		})
	}
}

// OpenCode roots its configuration at XDG_CONFIG_HOME, so setup writes there and launch checks the same file. A
// provider left in the opencode.json beside it goes, and both files are written with backups.
func TestOpencodeFollowsXDGConfigHome(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	xdg := filepath.Join(t.TempDir(), "xdg")
	s.env.ConfigHome = xdg
	s.env.Environ = append(s.env.Environ, "XDG_CONFIG_HOME="+xdg)
	plain, jsonc := filepath.Join(xdg, "opencode/opencode.json"), filepath.Join(xdg, "opencode/opencode.jsonc")
	writeFile(t, plain, `{"theme":"dark","provider":{"aiven":{"models":{"old":{}}}}}`)
	writeFile(t, jsonc, "{}\n")
	r := s.run("setup", "opencode", "--yes")
	if r.code != 0 || !strings.Contains(r.stderr, "written: "+plain+" (previous version: ") ||
		!strings.Contains(r.stderr, "written: "+jsonc+" (previous version: ") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if _, err := os.Stat(filepath.Join(s.fakeHome, ".config/opencode")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("setup wrote under ~/.config: %v", err)
	}
	if got := readJSON(t, plain); !reflect.DeepEqual(got, map[string]any{"theme": "dark"}) {
		t.Errorf("opencode.json = %v", got)
	}
	if providers, _ := readJSON(t, jsonc)["provider"].(map[string]any); providers["aiven"] == nil {
		t.Errorf("opencode.jsonc has no provider: %v", providers)
	}
	if r := s.run("launch", "opencode"); r.code != 0 || len(s.runner.calls) != 1 {
		t.Fatalf("launch: exit %d, stderr %q", r.code, r.stderr)
	}
	s.env.ConfigHome = ""
	if r := s.run("launch", "opencode"); r.code != 1 || !strings.Contains(r.stderr, "opencode setup is missing") {
		t.Errorf("launch without XDG_CONFIG_HOME: exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestOpencodeLaunchNeedsTheKeyFile(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).configured("opencode")
	before := len(g.journal())
	s.env.Environ = append(s.env.Environ, "AIVEN_AI_GATEWAY_KEY="+keyValid)
	r := s.run("launch", "opencode")
	if r.code != 1 || r.stderr != "Gateway: "+s.env.BaseURL+"\naiven-ai-gateway: OpenCode requires a key saved for this gateway. Run: aiven-ai-gateway login\n" {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if len(s.runner.calls) != 0 || len(g.journal()) != before {
		t.Errorf("exec'd %v, %d gateway requests", s.runner.calls, len(g.journal())-before)
	}
}
