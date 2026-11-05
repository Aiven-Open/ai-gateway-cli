// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/cli"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/omp"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/pi"
)

func TestListedModelsAppearInCLIAndCatalogueConfigurations(t *testing.T) {
	t.Parallel()
	g := newGatewayDouble(t)
	g.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"private-customer-model","display_name":"Private customer display","owned_by":"Private customer"},{"id":"claude-sonnet"},{"id":"gpt-6.1-sol"},{"id":"gpt-oss-120b"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[],"content":[],"output":[]}`))
	})
	for _, tool := range []string{"pi", "omp", "claude", "codex", "opencode"} {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			s := newSession(t, g).stdin(keyValid)
			for _, args := range [][]string{
				{"login"},
				{"models"},
				{"setup", tool, "--yes"},
				{"launch", tool},
			} {
				r := s.run(args...)
				if r.code != 0 {
					t.Fatalf("%v: code=%d stderr=%s", args, r.code, r.stderr)
				}
				if (args[0] == "login" || args[0] == "models") && !strings.Contains(r.stdout, "private-customer-model") {
					t.Fatalf("%v did not show a listed model", args)
				}
			}
			paths := []string{filepath.Join(s.fakeHome, toolFiles[tool])}
			if tool == "codex" {
				paths = append(paths, filepath.Join(s.fakeHome, ".codex", "aiven.config.toml"))
			}
			for _, path := range paths {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if (tool == "pi" || tool == "omp" || tool == "opencode") && !strings.Contains(string(data), "private-customer-model") {
					t.Fatal("catalogue configuration omitted a listed entry")
				}
			}
		})
	}
}

func TestCancellationAfterASuccessfulProtocolPreventsAllWrites(t *testing.T) {
	t.Parallel()
	g := newGatewayDouble(t)
	s := newSession(t, g).stdin(keyValid)
	s.deps.Prompter = &scriptedPrompter{answers: []string{"all"}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	g.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet"},{"id":"gpt-6.1-sol"},{"id":"gpt-oss-120b"}]}`))
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			cancel()
		}
		_, _ = w.Write([]byte(`{"choices":[],"content":[],"output":[]}`))
	})
	if code := cli.Run(ctx, []string{"setup", "--key-stdin", "--yes"}, s.env, s.deps); code != 1 {
		t.Fatalf("cancelled setup code = %d", code)
	}
	for _, rel := range toolFiles {
		if _, err := os.Stat(filepath.Join(s.fakeHome, rel)); !os.IsNotExist(err) {
			t.Errorf("cancelled setup wrote %s", rel)
		}
	}
	if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
		t.Fatal("cancelled setup saved a key")
	}
}

func TestFailedProtocolCheckPreservesSavedKeyAndReportsNoNewSave(t *testing.T) {
	t.Parallel()
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyNoInfer)
	keyBefore, err := os.ReadFile(s.keyPath())
	if err != nil {
		t.Fatal(err)
	}
	r := s.run("setup", "claude", "--yes")
	if r.code != 1 || !strings.Contains(r.stderr, "not saved by this run; existing key files unchanged") {
		t.Fatalf("misleading failed-check summary: code=%d stderr=%s", r.code, r.stderr)
	}
	if after, err := os.ReadFile(s.keyPath()); err != nil || string(after) != string(keyBefore) {
		t.Fatalf("failed check changed existing credential: %v", err)
	}
}

func TestSetupChecksConfiguredProtocolsAndWritesOnlyReadyTools(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"stdin", "saved", "environment"} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			g := newGatewayDouble(t)
			s := newSession(t, g).stdin(keyValid)
			var mu sync.Mutex
			counts := map[string]int{}
			g.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				counts[r.URL.Path]++
				mu.Unlock()
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet"},{"id":"gpt-6.1-sol"},{"id":"gpt-oss-120b"}]}`))
					return
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				for _, forbidden := range []string{"store", "stream", "tools"} {
					if _, ok := body[forbidden]; ok {
						t.Errorf("minimal probe included %s", forbidden)
					}
				}
				switch r.URL.Path {
				case "/v1/chat/completions":
					// Pi and OMP check Chat Completions with gpt-oss-120b, OpenCode with claude-sonnet: one model's
					// refusal must not fail a tool that checked another.
					if body["model"] != "gpt-oss-120b" && body["model"] != "claude-sonnet" || body["max_tokens"] != float64(1) {
						t.Errorf("chat body = %v", body)
					}
					if body["model"] == "claude-sonnet" {
						_, _ = w.Write([]byte(`{"object":"chat.completion","choices":[]}`))
						break
					}
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":{"message":"Unrecognized request argument supplied: store"}}`))
				case "/v1/messages":
					if body["model"] != "claude-sonnet" || body["max_tokens"] != float64(1) {
						t.Errorf("messages body = %v", body)
					}
					_, _ = w.Write([]byte(`{"type":"message","content":[]}`))
				case "/v1/responses":
					if body["model"] != "gpt-6.1-sol" || body["max_output_tokens"] != float64(1) {
						t.Errorf("responses body = %v", body)
					}
					_, _ = w.Write([]byte(`{"object":"response","output":[]}`))
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			args := []string{"setup", "--yes"}
			switch source {
			case "stdin":
				args = append(args, "--key-stdin")
			case "saved":
				s.loggedIn(keyValid)
			case "environment":
				s.env.Environ = append(s.env.Environ, "AIVEN_AI_GATEWAY_KEY="+keyValid)
			}
			answers := []string{"all"}
			if source == "environment" {
				answers = append(answers, "y")
			}
			s.deps.Prompter = &scriptedPrompter{answers: answers}
			for _, tool := range []string{"pi", "omp", "opencode"} {
				writeFile(t, filepath.Join(s.fakeHome, toolFiles[tool]), "{}")
			}
			r := s.run(args...)
			if r.code != 1 {
				t.Fatalf("partial failure code = %d: %s", r.code, r.stderr)
			}
			for _, want := range []string{"Chat Completions", "Messages", "Responses", "HTTP 400", "HTTP 200", "Unrecognized request argument supplied: store", "basic check", "tool's own requests"} {
				if !strings.Contains(r.stderr, want) {
					t.Errorf("missing %q in %s", want, r.stderr)
				}
			}
			if strings.Contains(r.stderr, "refused that key") || strings.Contains(r.stderr, "rejected the key") || strings.Contains(r.stderr, keyValid) {
				t.Errorf("request refusal misclassified or key exposed: %s", r.stderr)
			}
			for _, tool := range []string{"pi", "omp"} {
				if data, err := os.ReadFile(filepath.Join(s.fakeHome, toolFiles[tool])); err != nil || string(data) != "{}" {
					t.Errorf("failed %s changed: %q %v", tool, data, err)
				}
			}
			if data, err := os.ReadFile(filepath.Join(s.fakeHome, toolFiles["opencode"])); err != nil || string(data) == "{}" {
				t.Errorf("ready opencode not written: %q %v", data, err)
			}
			for _, tool := range []string{"claude", "codex"} {
				if _, err := os.Stat(filepath.Join(s.fakeHome, toolFiles[tool])); err != nil {
					t.Errorf("ready %s not written: %v", tool, err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			for endpoint, want := range map[string]int{"/v1/models": 1, "/v1/chat/completions": 2, "/v1/messages": 1, "/v1/responses": 1} {
				if counts[endpoint] != want {
					t.Errorf("%s count = %d, want %d: one per distinct protocol and model", endpoint, counts[endpoint], want)
				}
			}
		})
	}
}

func TestPiAndOmpConfigureModelProtocolsWithoutCompatibilityOverrides(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"pi", "omp"} {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			g := newGatewayDouble(t)
			s := newSession(t, g).stdin(keyValid)
			g.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet"},{"id":"gpt-6.1-sol"},{"id":"gpt-oss-120b"},{"id":"deepseek-v3.2"}]}`))
					return
				}
				_, _ = w.Write([]byte(`{"choices":[],"content":[],"output":[]}`))
			})
			r := s.run("setup", tool, "--key-stdin", "--yes")
			if r.code != 0 {
				t.Fatal(r.stderr)
			}
			path := filepath.Join(s.fakeHome, toolFiles[tool])
			var cfg map[string]any
			if tool == "omp" {
				cfg = readYAML(t, path)
			} else {
				cfg = readJSON(t, path)
			}
			provider := cfg["providers"].(map[string]any)["aiven"].(map[string]any)
			entries, ok := provider["models"].([]any)
			if !ok || len(entries) != 4 {
				t.Fatalf("missing explicit protocol models: %v", provider)
			}
			want := map[string]string{"claude-sonnet": "anthropic-messages", "gpt-6.1-sol": "openai-responses", "gpt-oss-120b": "openai-completions", "deepseek-v3.2": "openai-completions"}
			for _, entry := range entries {
				m := entry.(map[string]any)
				api := want[m["id"].(string)]
				base := g.URL + "/v1"
				if api == "anthropic-messages" {
					base = g.URL
				}
				if m["api"] != api || m["baseUrl"] != base {
					t.Errorf("route = %v, want %s %s", m, api, base)
				}
				if _, exists := m["compat"]; exists {
					t.Errorf("generated compat: %v", m)
				}
			}
			if _, exists := provider["compat"]; exists {
				t.Errorf("generated provider compat: %v", provider)
			}
			if tool == "omp" {
				if _, err := omp.Ready(s.fakeHome, g.URL); err != nil {
					t.Fatal(err)
				}
			} else if _, err := pi.Ready(s.fakeHome, g.URL); err != nil {
				t.Fatal(err)
			}
			first := entries[0].(map[string]any)
			originalAPI, originalOrigin := first["api"], first["baseUrl"]
			for _, field := range []string{"api", "baseUrl"} {
				first[field] = "incorrect"
				writeFile(t, path, string(harness.JSON(cfg)))
				var err error
				if tool == "omp" {
					_, err = omp.Ready(s.fakeHome, g.URL)
				} else {
					_, err = pi.Ready(s.fakeHome, g.URL)
				}
				if err == nil {
					t.Errorf("Ready accepted incorrect model %s", field)
				}
				first["api"], first["baseUrl"] = originalAPI, originalOrigin
			}
		})
	}
}

func TestUnknownOnlyCatalogueUsesChatRegardlessOfDisplayOwner(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"pi", "omp"} {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			g := newGatewayDouble(t)
			s := newSession(t, g).stdin(keyValid)
			g.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"data":[{"id":"private-router","owned_by":"Anthropic","display_name":"Claude Haiku"}]}`))
					return
				}
				if r.URL.Path != "/v1/chat/completions" {
					t.Errorf("wrong inferred protocol %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"choices":[]}`))
			})
			r := s.run("setup", tool, "--key-stdin", "--yes")
			if r.code != 0 || !strings.Contains(r.stderr, "Chat Completions with private-router") {
				t.Fatalf("code=%d stderr=%s", r.code, r.stderr)
			}
			for _, path := range []string{s.keyPath(), filepath.Join(s.fakeHome, toolFiles[tool])} {
				if _, err := os.Stat(path); err != nil {
					t.Errorf("listed route missing %s: %v", path, err)
				}
			}
		})
	}
}

func TestBasicChecksUseEachToolsConfiguredModel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, tool, file, content, catalogue string
		want                                 map[string]string
	}{
		{"pi prefers the default representatives", "pi", "", "",
			`[{"id":"claude-haiku"},{"id":"claude-sonnet"},{"id":"gpt-5-mini"},{"id":"gpt-6.1-sol"},{"id":"gpt-oss-120b"}]`,
			map[string]string{"/v1/messages": "claude-sonnet", "/v1/responses": "gpt-6.1-sol", "/v1/chat/completions": "gpt-oss-120b"}},
		{"codex checks its profile model", "codex", ".codex/aiven.config.toml", "model = \"gpt-5-mini\"\n",
			`[{"id":"gpt-5-mini"},{"id":"gpt-6.1-sol"}]`, map[string]string{"/v1/responses": "gpt-5-mini"}},
		{"claude checks its pinned model without the context suffix", "claude", ".claude/aiven.settings.json", `{"model":"claude-opus-5-5[1m]"}`,
			`[{"id":"claude-opus-5-5"},{"id":"claude-sonnet"}]`, map[string]string{"/v1/messages": "claude-opus-5-5"}},
		{"claude resolves a native alias through its slot", "claude", ".claude/aiven.settings.json", `{"model":"opus"}`,
			`[{"id":"claude-opus-5-5"},{"id":"claude-sonnet"}]`, map[string]string{"/v1/messages": "claude-opus-5-5"}},
		{"claude checks a kept ANTHROPIC_MODEL over the model", "claude", ".claude/aiven.settings.json",
			`{"model":"claude-sonnet","env":{"ANTHROPIC_MODEL":"claude-opus-5-5[1M]"}}`,
			`[{"id":"claude-opus-5-5"},{"id":"claude-sonnet"}]`, map[string]string{"/v1/messages": "claude-opus-5-5"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := newGatewayDouble(t)
			var mu sync.Mutex
			got := map[string]string{}
			g.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"data":` + tc.catalogue + `}`))
					return
				}
				var body struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				mu.Lock()
				got[r.URL.Path] = body.Model
				mu.Unlock()
				_, _ = w.Write([]byte(`{"choices":[],"content":[],"output":[]}`))
			})
			s := newSession(t, g).stdin(keyValid)
			if tc.file != "" {
				writeFile(t, filepath.Join(s.fakeHome, tc.file), tc.content)
			}
			if r := s.run("setup", tc.tool, "--key-stdin", "--yes"); r.code != 0 {
				t.Fatalf("code=%d stderr=%s", r.code, r.stderr)
			}
			mu.Lock()
			defer mu.Unlock()
			if !maps.Equal(got, tc.want) {
				t.Errorf("checked models %v, want %v", got, tc.want)
			}
		})
	}
}

// A tool whose configured model the key does not grant would keep requesting it, and the key is shared, so setup saves
// neither the key nor any tool, and sends no inference request.
func TestSetupRefusesAConfiguredModelTheKeyDoesNotGrant(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, file, content string
		tools               []string
	}{
		{"codex profile beside pi", ".codex/aiven.config.toml", "model = \"gpt-retired\"\n", []string{"codex", "pi"}},
		{"claude model beside pi", ".claude/aiven.settings.json", `{"model":"claude-retired[1m]"}`, []string{"claude", "pi"}},
		{"claude ANTHROPIC_MODEL", ".claude/aiven.settings.json", `{"model":"claude-sonnet","env":{"ANTHROPIC_MODEL":"claude-retired"}}`, []string{"claude"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := newGatewayDouble(t)
			s := newSession(t, g).stdin(keyValid)
			writeFile(t, filepath.Join(s.fakeHome, tc.file), tc.content)
			args := []string{"setup", tc.tools[0], "--key-stdin", "--yes"}
			if len(tc.tools) > 1 {
				// Several tools are picked interactively; Enter keeps each native default.
				s.deps.Prompter = &scriptedPrompter{answers: []string{strings.Join(tc.tools, ","), "", ""}}
				args = []string{"setup", "--key-stdin"}
			}
			r := s.run(args...)
			if r.code == 0 || !strings.Contains(r.stderr, "claude-retired") && !strings.Contains(r.stderr, "gpt-retired") ||
				!strings.Contains(r.stderr, "does not grant") {
				t.Fatalf("exit %d, stderr %s", r.code, r.stderr)
			}
			for _, req := range g.journal() {
				if req.Method != http.MethodGet {
					t.Errorf("sent %s %s", req.Method, req.URL.Path)
				}
			}
			if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
				t.Errorf("key saved: %v", err)
			}
			if data, _ := os.ReadFile(filepath.Join(s.fakeHome, tc.file)); string(data) != tc.content {
				t.Errorf("%s changed: %s", tc.file, data)
			}
			if _, err := os.Stat(filepath.Join(s.fakeHome, ".pi/agent/models.json")); !os.IsNotExist(err) {
				t.Errorf("pi configured: %v", err)
			}
		})
	}
}

// With the gateway as the native default, setup also writes the user's settings.json, whose model must be granted too.
func TestSetupChecksTheModelOfEveryClaudeSettingsFileItWrites(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).stdin(keyValid)
	s.writeUserSettings("settings.json", `{"model":"claude-retired"}`)
	s.deps.Prompter = &callbackPrompter{answers: []string{"y"}}
	r := s.run("setup", "claude", "--key-stdin")
	if r.code == 0 || !strings.Contains(r.stderr, "claude-retired") || !strings.Contains(r.stderr, "does not grant") {
		t.Fatalf("exit %d, stdout %s, stderr %s", r.code, r.stdout, r.stderr)
	}
	if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
		t.Errorf("key saved: %v", err)
	}
}

func TestAuthenticationFailureStopsTheRemainingChecks(t *testing.T) {
	t.Parallel()
	g := newGatewayDouble(t)
	var mu sync.Mutex
	var sent []string
	g.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet"},{"id":"gpt-6.1-sol"},{"id":"gpt-oss-120b"}]}`))
			return
		}
		mu.Lock()
		sent = append(sent, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Authentication Error"}}`))
	})
	s := newSession(t, g).stdin(keyValid)
	r := s.run("setup", "pi", "--key-stdin", "--yes")
	if r.code != 1 || !strings.Contains(r.stderr, "not attempted after authentication failure") {
		t.Fatalf("code=%d stderr=%s", r.code, r.stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 {
		t.Errorf("requests after the authentication failure: %v", sent)
	}
	if _, err := os.Stat(s.keyPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("key saved after an authentication failure: %v", err)
	}
}
