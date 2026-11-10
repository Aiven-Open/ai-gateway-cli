// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSetupOMPRedactsAcceptedYAMLValuesInBothPreviewViews(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		secrets    []string
	}{
		{"plain", "    apiKey: SYNTHETIC_plain value\n", []string{"SYNTHETIC_plain value"}},
		{"single quoted", "    password: 'SYNTHETIC_quoted value'\n", []string{"SYNTHETIC_quoted value"}},
		{"double quoted", "    token: \"SYNTHETIC_double value\"\n", []string{"SYNTHETIC_double value"}},
		{"literal", "    apiKey: |-\n      SYNTHETIC_first\n      SYNTHETIC_second\n", []string{"SYNTHETIC_first", "SYNTHETIC_second"}},
		{"folded", "    secret: >-\n      SYNTHETIC_folded\n      SYNTHETIC_next\n", []string{"SYNTHETIC_folded", "SYNTHETIC_next"}},
		{"continued plain", "    apiKey: SYNTHETIC_continued\n      SYNTHETIC_tail\n", []string{"SYNTHETIC_continued", "SYNTHETIC_tail"}},
		{"flow", "    auth: {password: SYNTHETIC_flow, token: SYNTHETIC_token}\n", []string{"SYNTHETIC_flow", "SYNTHETIC_token"}},
		{"untrusted reference", "    apiKey: OPENAI_API_KEY\n", []string{"OPENAI_API_KEY"}},
		{"anchored credential", "    apiKey: &credential SYNTHETIC_anchored value\n    copy: *credential\n", []string{"SYNTHETIC_anchored value"}},
		{"aliased credential", "    defaultValue: &credential SYNTHETIC_aliased value\n    apiKey: *credential\n", []string{"SYNTHETIC_aliased value"}},
		{"alias in sequence", "    apiKey: &credential SYNTHETIC_sequence_secret\n    values: [*credential]\n", []string{"SYNTHETIC_sequence_secret"}},
		{"anchor in sequence", "    values:\n      - &credential SYNTHETIC_sequence_secret\n    apiKey: *credential\n", []string{"SYNTHETIC_sequence_secret"}},
		{"nested sequence", "    apiKey: &credential SYNTHETIC_nested_secret\n    values: [[*credential]]\n", []string{"SYNTHETIC_nested_secret"}},
		{"numeric sequence", "    values: [&credential 76543210]\n    apiKey: *credential\n", []string{"76543210"}},
		{"block sequence", "    values:\n      - &credential |-\n          SYNTHETIC_sequence_first\n          SYNTHETIC_sequence_second\n    apiKey: *credential\n", []string{"SYNTHETIC_sequence_first", "SYNTHETIC_sequence_second"}},
		{"tagged", "    apiKey: !!str SYNTHETIC_tagged\n", []string{"SYNTHETIC_tagged"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, full := range []bool{false, true} {
				s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
				path := s.ompModelsYML()
				original := "providers:\n  unrelated:\n" + tc.body + "    baseUrl: https://example.invalid/v1\n"
				writeFile(t, path, original)
				args := []string{"setup", "omp", "--yes"}
				if full {
					s.deps.Prompter = &callbackPrompter{answers: []string{"", "", "v", "n"}}
					args = []string{"setup", "omp"}
				}
				r := s.run(args...)
				if r.code != 0 || !strings.Contains(r.stderr, "--- "+path) {
					t.Fatalf("full=%v: setup did not reach preview: %s", full, r.stderr)
				}
				for _, secret := range tc.secrets {
					if strings.Contains(r.stdout+r.stderr, secret) {
						t.Fatalf("full=%v: existing YAML credential appeared in output", full)
					}
				}
				if full && strings.Count(r.stderr, "--- "+path) != 2 {
					t.Fatal("full diff was not displayed")
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if full && string(data) != original {
					t.Fatal("decline changed the original YAML")
				}
				if !full && !strings.Contains(string(data), tc.secrets[0]) {
					t.Fatal("redaction changed the stored unrelated credential")
				}
			}
		})
	}
}

func TestCodexSetupPreservesExistingDedicatedProfile(t *testing.T) {
	t.Parallel()
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	path := filepath.Join(s.fakeHome, ".codex/aiven.config.toml")
	writeFile(t, path, `model = 'claude-opus'
model_provider = 'aiven'
sandbox_mode = 'read-only'
model_reasoning_effort = 'high'
[mcp_servers.canonical]
command = 'example'
args = ['literal stable']
[model_providers.unrelated]
name = 'Keep this provider'
[model_providers.aiven]
request_max_retries = 7
env_key = 'OLD_CREDENTIAL_REFERENCE'
`)
	var before map[string]any
	codexFile(t, s, "aiven.config.toml", &before)
	if r := s.run("setup", "codex", "--yes"); r.code != 0 {
		t.Fatal(r.stderr)
	}
	var after map[string]any
	codexFile(t, s, "aiven.config.toml", &after)
	for _, field := range []string{"model", "sandbox_mode", "model_reasoning_effort", "mcp_servers"} {
		if !reflect.DeepEqual(before[field], after[field]) {
			t.Fatalf("dedicated profile lost %s", field)
		}
	}
	providers := after["model_providers"].(map[string]any)
	aiven := providers["aiven"].(map[string]any)
	if providers["unrelated"] == nil || aiven["request_max_retries"] != int64(7) || aiven["env_key"] != nil {
		t.Fatal("provider merge lost unrelated settings or kept conflicting key wiring")
	}
	beforeLaunch := configurationSnapshot(t, s.fakeHome, s.env.WorkDir)
	if r := s.run("launch", "codex"); r.code != 0 {
		t.Fatal(r.stderr)
	}
	if afterLaunch := configurationSnapshot(t, s.fakeHome, s.env.WorkDir); !reflect.DeepEqual(beforeLaunch, afterLaunch) {
		t.Fatal("launch changed the preserved dedicated profile")
	}
}

func TestCodexSetupRejectsMalformedConsumedProfileBeforeEffects(t *testing.T) {
	t.Parallel()
	for _, original := range []string{"model = [broken", "model_providers = 'not a table'"} {
		t.Run(original, func(t *testing.T) {
			t.Parallel()
			g := newGatewayDouble(t)
			s := newSession(t, g).stdin(keyValid)
			path := filepath.Join(s.fakeHome, ".codex/aiven.config.toml")
			writeFile(t, path, original)
			before := configurationSnapshot(t, s.fakeHome, s.env.WorkDir)
			if r := s.run("setup", "codex", "--key-stdin", "--yes"); r.code == 0 || len(g.journal()) != 0 {
				t.Fatalf("invalid consumed profile reached gateway: %s", r.stderr)
			}
			if after := configurationSnapshot(t, s.fakeHome, s.env.WorkDir); !reflect.DeepEqual(before, after) {
				t.Fatal("invalid consumed profile changed files or credentials")
			}
		})
	}
}

func TestClaudeLaunchResolvesCanonicalUserAndProjectPins(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, user, project, local, want string
	}{
		{"user model", `{"model":"claude-opus"}`, `{}`, `{}`, "claude-opus[1m]"},
		{"user env", `{"model":"glm","env":{"ANTHROPIC_MODEL":"claude-opus"}}`, `{}`, `{}`, "claude-opus[1m]"},
		{"project model", `{"model":"claude-opus"}`, `{"model":"claude-sonnet"}`, `{}`, "claude-sonnet[1m]"},
		{"project local", `{"model":"claude-opus"}`, `{"model":"claude-sonnet"}`, `{"env":{"ANTHROPIC_MODEL":"glm"}}`, "glm[1m]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
			if r := s.run("setup", "claude", "--yes"); r.code != 0 {
				t.Fatal(r.stderr)
			}
			writeFile(t, filepath.Join(s.fakeHome, ".claude/settings.json"), tc.user)
			writeFile(t, filepath.Join(s.env.WorkDir, ".claude/settings.json"), tc.project)
			writeFile(t, filepath.Join(s.env.WorkDir, ".claude/settings.local.json"), tc.local)
			before := configurationSnapshot(t, s.fakeHome, s.env.WorkDir)
			if r := s.run("launch", "claude"); r.code != 0 {
				t.Fatal(r.stderr)
			}
			call := s.runner.calls[0]
			var inline map[string]any
			if err := json.Unmarshal([]byte(call.argv[2]), &inline); err != nil {
				t.Fatal(err)
			}
			if inline["model"] != tc.want {
				t.Fatalf("inline model=%v overrides effective pin %s", inline["model"], tc.want)
			}
			if model, _ := call.envVar("ANTHROPIC_MODEL"); model != tc.want {
				t.Fatal("child environment and inline selected models disagree")
			}
			if after := configurationSnapshot(t, s.fakeHome, s.env.WorkDir); !reflect.DeepEqual(before, after) {
				t.Fatal("resolving native pins changed configuration")
			}
		})
	}
}
