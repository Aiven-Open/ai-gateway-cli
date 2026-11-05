// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func (s *session) ompModelsYML() string { return filepath.Join(s.fakeHome, ".omp/agent/models.yml") }

func TestOmpConsumedYMLWinsAndInvalidStopsBeforeNetwork(t *testing.T) {
	for _, file := range []string{"models", "config"} {
		t.Run(file, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g).stdin(keyValid)
			path := filepath.Join(s.fakeHome, ".omp/agent", file+".yml")
			writeFile(t, path, "[invalid mapping")
			writeFile(t, filepath.Join(s.fakeHome, ".omp/agent", file+".yaml"), "{}\n")
			r := s.run("setup", "omp", "--key-stdin", "--yes")
			if r.code != 1 || len(g.journal()) != 0 || !strings.Contains(r.stderr, path) {
				t.Fatalf("%d %s requests=%d", r.code, r.stderr, len(g.journal()))
			}
			if got, _ := os.ReadFile(path); string(got) != "[invalid mapping" {
				t.Fatal("malformed consumed YAML changed")
			}
		})
	}
}

func TestOmpMigrationRerunUsesNativeConsumedYAML(t *testing.T) {
	for _, suffix := range []string{".yml", ".yaml"} {
		t.Run(suffix, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
			dir := filepath.Join(s.fakeHome, ".omp/agent")
			writeFile(t, filepath.Join(dir, "models.json"), `{"preserve":"old"}`)
			writeFile(t, filepath.Join(dir, "settings.json"), `{"preserve":"old"}`)
			writeFile(t, filepath.Join(dir, "models"+suffix), "providers: {}\n")
			writeFile(t, filepath.Join(dir, "config"+suffix), "theme: native-migrated\n")
			if r := s.run("setup", "omp", "--key-stdin", "--yes"); r.code != 0 {
				t.Fatalf("%d %s", r.code, r.stderr)
			}
		})
	}
}

func TestOmpNativeArgumentsKeepTheirPrecedence(t *testing.T) {
	for _, args := range [][]string{{"--model", "other/literal model", "-p", "hi"}, {"--model=other/model"}, {"config", "list"}, {"plugin", "list"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
			if r := s.setupAndLaunch("omp", args...); r.code != 0 {
				t.Fatalf("%d %s", r.code, r.stderr)
			}
			if got := s.launched().argv; !slices.Equal(got, append([]string{"omp"}, args...)) {
				t.Fatalf("native override/subcommand changed: %q", got)
			}
		})
	}
}

func TestOmpNoAndYesFlagLeaveConfigBytesUnchanged(t *testing.T) {
	for _, yesFlag := range []bool{false, true} {
		t.Run(map[bool]string{false: "default no", true: "yes flag"}[yesFlag], func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
			path := filepath.Join(s.fakeHome, ".omp/agent/config.yml")
			original := "# exact preserved bytes\nmodelRoles: {default: other/model}\nenabledModels: [other/*]\n"
			writeFile(t, path, original)
			args := []string{"setup", "omp", "--key-stdin"}
			if yesFlag {
				args = append(args, "--yes")
			} else {
				s.deps.Prompter = &callbackPrompter{answers: []string{"", "y"}}
			}
			if r := s.run(args...); r.code != 0 {
				t.Fatalf("%d %s", r.code, r.stderr)
			}
			if got, _ := os.ReadFile(path); string(got) != original {
				t.Fatal("default-no changed settings bytes")
			}
		})
	}
}

func TestOmpOwnedYAMLMappingBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, original string
		wantFailure    bool
	}{
		{"quoted keys", "'providers':\n  'aiven': {old: value}\n  unrelated: {apiKey: SYNTHETIC_keep}\n", false},
		{"anchored providers", "providers: &all\n  unrelated: {apiKey: SYNTHETIC_keep}\n", false},
		{"shared anchored providers", "providers: &all\n  unrelated: {apiKey: SYNTHETIC_keep}\ncopy: *all\n", true},
		{"aliased providers", "source: &all\n  unrelated: {apiKey: SYNTHETIC_keep}\nproviders: *all\n", true},
		{"owned anchor referenced elsewhere", "providers:\n  aiven: &old {apiKey: SYNTHETIC_keep}\ncopy: *old\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g).stdin(keyValid)
			path := s.ompModelsYML()
			writeFile(t, path, tc.original)
			r := s.run("setup", "omp", "--key-stdin", "--yes")
			if (r.code != 0) != tc.wantFailure || strings.Contains(r.stderr, "SYNTHETIC_keep") {
				t.Fatalf("%d %s", r.code, r.stderr)
			}
			if tc.wantFailure {
				if len(g.journal()) != 0 {
					t.Fatal("unsafe YAML edit reached the gateway")
				}
				if got, _ := os.ReadFile(path); string(got) != tc.original {
					t.Fatal("unsafe YAML edit changed the original")
				}
			} else {
				providers := readYAML(t, path)["providers"].(map[string]any)
				if providers["unrelated"].(map[string]any)["apiKey"] != "SYNTHETIC_keep" {
					t.Fatal("unrelated provider changed")
				}
			}
		})
	}
}

func TestOmpRelocationRefusesBeforeCredentialsAndNetwork(t *testing.T) {
	for _, name := range []string{"PI_CODING_AGENT_DIR", "OMP_PROFILE", "PI_PROFILE", "PI_CONFIG_DIR"} {
		for _, verb := range []string{"setup", "launch"} {
			t.Run(name+"/"+verb, func(t *testing.T) {
				g := newGatewayDouble(t)
				s := newSession(t, g).stdin(keyValid)
				s.env.Environ = append(s.env.Environ, name+"=relocated")
				if r := s.run(verb, "omp"); r.code != 1 || !strings.Contains(r.stderr, name+" is set") {
					t.Fatalf("%d %s", r.code, r.stderr)
				}
				if len(g.journal()) != 0 || len(s.runner.calls) != 0 || len(s.homeFiles()) != 0 {
					t.Fatal("relocation refusal has side effects")
				}
			})
		}
	}
}

func TestOmpRequiresNormalSetupBeforeRequests(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	if r := s.run("launch", "omp"); r.code != 1 || !strings.Contains(r.stderr, "setup omp") {
		t.Fatalf("%d %s", r.code, r.stderr)
	}
	if len(g.journal()) != 0 || len(s.runner.calls) != 0 || len(s.homeFiles()) != 0 {
		t.Fatal("missing setup contacted gateway, launched, or wrote files")
	}
}

func TestOmpYAMLSuffixCommentsAnchorsAndScopePreserved(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	dir := filepath.Join(s.fakeHome, ".omp/agent")
	modelsPath, configPath := filepath.Join(dir, "models.yaml"), filepath.Join(dir, "config.yaml")
	modelsBefore := "# provider heading\nproviders:\n  unrelated:\n    apiKey: &credential SYNTHETIC_secret\n    values: [*credential] # alias comment\n    baseUrl: https://example.invalid/v1\n"
	configBefore := "# scope heading\nenabledModels: [other/*] # scope comment\ndisabledProviders: [aiven]\nmodelRoles:\n  default: other/model # default comment\n  architect: &role other/architect\n  task: *role\n"
	writeFile(t, modelsPath, modelsBefore)
	writeFile(t, configPath, configBefore)
	s.deps.Prompter = &callbackPrompter{answers: []string{"y", "y"}}
	r := s.run("setup", "omp", "--key-stdin")
	if r.code != 0 || strings.Contains(r.stderr, "SYNTHETIC_secret") || !strings.Contains(r.stderr, "scope can override") {
		t.Fatalf("%d %s", r.code, r.stderr)
	}
	config := readYAML(t, configPath)
	roles := config["modelRoles"].(map[string]any)
	if roles["default"] != "aiven/claude-sonnet" || roles["task"] != "other/architect" ||
		!slices.Equal(config["enabledModels"].([]any), []any{"other/*"}) ||
		!slices.Equal(config["disabledProviders"].([]any), []any{"aiven"}) {
		t.Fatalf("owned default or unrelated settings: %v", config)
	}
	for path, comments := range map[string][]string{
		modelsPath: {"# provider heading", "&credential", "*credential", "# alias comment", "SYNTHETIC_secret"},
		configPath: {"# scope heading", "# scope comment", "# default comment", "&role", "*role"},
	} {
		data, _ := os.ReadFile(path)
		for _, want := range comments {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s lost %s", path, want)
			}
		}
	}
	for _, name := range []string{"models.yml", "config.yml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("shadowed .yaml: %s %v", name, err)
		}
	}
}

func TestPiAndOmpDefaultQuestionsAreIndependent(t *testing.T) {
	for _, piDefault := range []bool{false, true} {
		t.Run(map[bool]string{false: "pi no omp yes", true: "pi yes omp no"}[piDefault], func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
			piAnswer, ompAnswer := "n", "y"
			if piDefault {
				piAnswer, ompAnswer = "y", "n"
			}
			p := &scriptedPrompter{answers: []string{"pi,omp", piAnswer, ompAnswer, "y", "y"}}
			s.deps.Prompter = p
			if r := s.run("setup", "--key-stdin"); r.code != 0 {
				t.Fatalf("%d %s prompts=%q", r.code, r.stderr, p.prompts)
			}
			for tool, path := range map[string]string{"pi": ".pi/agent/settings.json", "omp": ".omp/agent/config.yml"} {
				_, err := os.Stat(filepath.Join(s.fakeHome, path))
				want := piDefault
				if tool == "omp" {
					want = !want
				}
				if (err == nil) != want {
					t.Fatalf("%s default write: %v want=%v", tool, err, want)
				}
			}
			for _, tool := range []string{"Pi", "OMP"} {
				if !slices.Contains(p.prompts, "Make Aiven the native default for "+tool+"? Enter keeps your current default. [y/N] ") {
					t.Fatalf("missing independent %s question: %q", tool, p.prompts)
				}
			}
		})
	}
}

func TestOmpInitIsRejectedWithoutSideEffects(t *testing.T) {
	for _, args := range [][]string{{"omp", "init"}, {"omp", "init", "--help"}, {"omp", "init", "--architect", "sk-pasted-secret-value"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g).loggedIn(keyValid)
			project := filepath.Join(s.env.WorkDir, ".omp/config.yml")
			writeFile(t, project, "modelRoles:\n  architect: aiven-gateway/x\n")
			before := configurationSnapshot(t, s.fakeHome, s.env.WorkDir)
			r := s.run(args...)
			if r.code != 1 || !strings.Contains(r.stderr, "omp init was removed") || strings.Contains(r.stderr, "sk-pasted") {
				t.Fatalf("%d %s", r.code, r.stderr)
			}
			after := configurationSnapshot(t, s.fakeHome, s.env.WorkDir)
			if len(g.journal()) != 0 || len(s.runner.calls) != 0 || !reflect.DeepEqual(before, after) {
				t.Fatal("removed initializer has side effects")
			}
		})
	}
}
