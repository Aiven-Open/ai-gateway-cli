// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/aiven-open/ai-gateway-cli/internal/harness/omp"
)

func readYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestPlainOMPUsesNormalHomeWithoutPluginOrDefaultWrite(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).stdin(keyValid)
	r := s.run("setup", "omp", "--key-stdin", "--yes")
	if r.code != 0 {
		t.Fatalf("setup: %d %s", r.code, r.stderr)
	}
	path := filepath.Join(s.fakeHome, ".omp/agent/models.yml")
	p := readYAML(t, path)["providers"].(map[string]any)["aiven"].(map[string]any)
	if p["apiKey"] != omp.APIKey(g.URL) || p["auth"] != "apiKey" {
		t.Fatalf("native helper/auth: %v", p)
	}
	for _, name := range []string{"config.yml", "config.yaml", "../profiles"} {
		if _, err := os.Stat(filepath.Join(s.fakeHome, ".omp/agent", name)); !os.IsNotExist(err) {
			t.Fatalf("unexpected default/router path %s: %v", name, err)
		}
	}
	if r := s.run("launch", "omp", "--", "-p", "literal prompt"); r.code != 0 {
		t.Fatalf("launch: %d %s", r.code, r.stderr)
	}
	if got := s.launched().argv; !slices.Equal(got, []string{"omp", "-p", "literal prompt"}) {
		t.Fatalf("plain native argv: %q", got)
	}
	if !strings.Contains(r.stderr, "omp --model aiven/claude-sonnet") ||
		!strings.Contains(r.stderr, "alias omp-aiven='aiven-ai-gateway launch omp --'") ||
		!strings.Contains(r.stderr, "With native arguments, add your own model flag for OMP.") {
		t.Fatalf("missing direct command or shortcut: %s", r.stderr)
	}
}

func TestPlainOMPLegacyMigrationRefusesBeforeNetworkOrWrites(t *testing.T) {
	for _, name := range []string{"models.json", "settings.json"} {
		t.Run(name, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g).stdin(keyValid)
			path := filepath.Join(s.fakeHome, ".omp/agent", name)
			writeFile(t, path, `{"keep":"legacy"}`)
			before := s.homeFiles()
			r := s.run("setup", "omp", "--key-stdin", "--yes")
			if r.code != 1 || !strings.Contains(r.stderr, "run omp once") || len(g.journal()) != 0 ||
				!slices.Equal(before, s.homeFiles()) {
				t.Fatalf("legacy preflight: %d %s requests=%d files=%v", r.code, r.stderr, len(g.journal()), s.homeFiles())
			}
			if got, _ := os.ReadFile(path); string(got) != `{"keep":"legacy"}` {
				t.Fatal("legacy JSON changed")
			}
		})
	}
}

func TestPlainOMPAgentDBAloneDoesNotBlock(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	path := filepath.Join(s.fakeHome, ".omp/agent/agent.db")
	writeFile(t, path, "synthetic database bytes")
	if r := s.run("setup", "omp", "--key-stdin", "--yes"); r.code != 0 {
		t.Fatalf("agent.db is not pending JSON migration: %d %s", r.code, r.stderr)
	}
	if got, _ := os.ReadFile(path); string(got) != "synthetic database bytes" {
		t.Fatal("database changed")
	}
}

func TestPlainOMPEmptyNativeSettingsKeepComments(t *testing.T) {
	for _, original := range []string{"", "# user settings heading\n# keep this note\n", "null # empty preferences\n", "~\n", "---\n# empty document\n", "---\n# empty document\n...\n"} {
		for _, asDefault := range []bool{false, true} {
			t.Run(fmt.Sprintf("%q/default=%v", original, asDefault), func(t *testing.T) {
				s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
				path := filepath.Join(s.fakeHome, ".omp/agent/config.yml")
				writeFile(t, path, original)
				answer := "n"
				if asDefault {
					answer = "y"
				}
				s.deps.Prompter = &callbackPrompter{answers: []string{answer, "y"}}
				if r := s.run("setup", "omp", "--key-stdin"); r.code != 0 {
					t.Fatalf("%d %s", r.code, r.stderr)
				}
				got, _ := os.ReadFile(path)
				if !asDefault && string(got) != original {
					t.Fatal("default-no changed native settings bytes")
				}
				if asDefault {
					for line := range strings.SplitSeq(original, "\n") {
						if _, after, ok := strings.Cut(line, "#"); ok && !strings.Contains(string(got), "#"+after) {
							t.Fatalf("lost comment %q: %s", line, got)
						}
					}
					roles := readYAML(t, path)["modelRoles"].(map[string]any)
					if roles["default"] != "aiven/claude-sonnet" {
						t.Fatalf("default: %v", roles)
					}
				}
			})
		}
	}
}
