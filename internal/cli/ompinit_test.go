// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func (s *session) ompDir() string { return filepath.Join(s.env.WorkDir, ".omp") }

func (s *session) readProject(rel string) string {
	s.t.Helper()
	b, err := os.ReadFile(filepath.Join(s.ompDir(), rel))
	if err != nil {
		s.t.Fatal(err)
	}
	return string(b)
}

func TestOmpInitWritesProjectConfigWithDefaultsAndReportsPaths(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).ompPlugin()
	r := s.run("omp", "init")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	cfg := s.readProject("config.yml")
	for _, want := range []string{
		"  architect: aiven-gateway/claude-opus\n", "  builder: aiven-gateway/claude-sonnet\n",
		"  cross-reviewer: aiven-gateway/claude-opus\n", "  bulk-reader: aiven-gateway/gpt-sol\n",
		"  boilerplate-writer: aiven-gateway/gpt-sol\n", "    scout: \"@bulk-reader\"\n",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config lacks %q:\n%s", want, cfg)
		}
	}
	if want := filepath.Join(s.ompDir(), "config.yml") + "\n" + filepath.Join(s.ompDir(), "gateway-router.state.json") + "\n" + filepath.Join(s.ompDir(), "rules", "gateway-router.md") + "\n"; r.stdout != want {
		t.Errorf("stdout %q, want %q", r.stdout, want)
	}
	if len(s.runner.calls) != 0 {
		t.Errorf("exec'd %v", s.runner.calls)
	}
}

func TestOmpInitWritesRuleAndStateAndRestoresEditedRule(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.run("omp", "init")
	rule := s.readProject("rules/gateway-router.md")
	for _, want := range []string{"alwaysApply: true\n", "agents: main\n"} {
		if !strings.Contains(rule, want) {
			t.Errorf("rule lacks %q", want)
		}
	}
	if state := s.readProject("gateway-router.state.json"); state != "{\n  \"version\": 1,\n  \"managed\": {}\n}\n" {
		t.Errorf("state %q", state)
	}
	if err := os.WriteFile(filepath.Join(s.ompDir(), "rules", "gateway-router.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.run("omp", "init")
	if got := s.readProject("rules/gateway-router.md"); got != rule {
		t.Errorf("rule not restored: %q", got)
	}
}

func TestOmpInitQualifiesFlagSelectorsWithOrWithoutPrefix(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	r := s.run("omp", "init", "--architect", "claude-sonnet", "--builder", "aiven-gateway/claude-opus", "--cross-reviewer", "gpt-sol", "--bulk-reader", "glm", "--boilerplate-writer", "claude-opus")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	cfg := s.readProject("config.yml")
	for _, want := range []string{
		"  architect: aiven-gateway/claude-sonnet\n", "  builder: aiven-gateway/claude-opus\n", "  cross-reviewer: aiven-gateway/gpt-sol\n",
		"  bulk-reader: aiven-gateway/glm\n", "  boilerplate-writer: aiven-gateway/claude-opus\n",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config lacks %q:\n%s", want, cfg)
		}
	}
}

func TestOmpInitRejectsUnknownAliasAndWritesNothing(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	r := s.run("omp", "init", "--architect", "nope-9000")
	if want := "aiven-llm: architect: \"nope-9000\" is not an alias your key grants. Run: aiven-llm models\n"; r.code != 1 || !strings.HasSuffix(r.stderr, want) {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if _, err := os.Stat(s.ompDir()); err == nil {
		t.Error("wrote .omp")
	}
}

func TestOmpInitRefusesRouterAsRole(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	r := s.run("omp", "init", "--builder", "aiven-model")
	if want := "aiven-llm: builder: aiven-model picks a model per request, so the role would not be pinned. Pick a model.\n"; r.code != 1 || !strings.HasSuffix(r.stderr, want) {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if _, err := os.Stat(s.ompDir()); err == nil {
		t.Error("wrote .omp")
	}
}

func TestOmpInitAdvisorModesAndReclaimOnOff(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	if r := s.run("omp", "init", "--advisor", "both", "--advisor-model", "claude-opus"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	cfg := s.readProject("config.yml")
	for _, want := range []string{"  advisor: aiven-gateway/claude-opus\n", "advisor:\n  enabled: true\n", "agentAdvisor:\n    builder: aiven-gateway/claude-opus\n"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config lacks %q:\n%s", want, cfg)
		}
	}
	if r := s.run("omp", "init", "--advisor", "off"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if cfg := s.readProject("config.yml"); strings.Contains(cfg, "dvisor") {
		t.Errorf("advisor keys left after off:\n%s", cfg)
	}
}

func TestOmpInitRejectsAdvisorOutsideEnum(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	r := s.run("omp", "init", "--advisor", "bogus")
	if want := "aiven-llm: --advisor must be off, main, builder or both\n"; r.code != 1 || !strings.HasSuffix(r.stderr, want) {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if _, err := os.Stat(s.ompDir()); err == nil {
		t.Error("wrote .omp")
	}
}

func TestOmpInitReportsFlagErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"flag without value", []string{"--builder", "claude-opus", "--architect"}, "--architect needs a value"},
		{"unknown flag", []string{"--bogus", "x"}, "unknown flag for omp init: --bogus"},
		{"positional argument", []string{"extra"}, "unknown flag for omp init: extra"},
		{"help is not a flag", []string{"--help"}, "unknown flag for omp init: --help"},
		{"help with value", []string{"--help=true"}, "unknown flag for omp init: --help=true"},
		{"short help with value", []string{"-h=false"}, "unknown flag for omp init: -h=false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g).loggedIn(keyValid)
			r := s.run(append([]string{"omp", "init"}, tc.args...)...)
			if want := "aiven-llm: " + tc.want + "\n"; r.code != 1 || r.stderr != want {
				t.Fatalf("exit %d, stderr %q, want %q", r.code, r.stderr, want)
			}
			if _, err := os.Stat(s.ompDir()); err == nil {
				t.Error("wrote .omp")
			}
		})
	}
}

func TestOmpInitAdvisorOffDropsAdvisorModel(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	if r := s.run("omp", "init", "--advisor", "off", "--advisor-model", "nope-9000"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if cfg := s.readProject("config.yml"); strings.Contains(cfg, "dvisor") {
		t.Errorf("advisor keys written:\n%s", cfg)
	}
}

func TestOmpInitAdvisorModelDefaultsToCrossReviewer(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	if r := s.run("omp", "init", "--advisor", "main", "--cross-reviewer", "gpt-sol"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if cfg := s.readProject("config.yml"); !strings.Contains(cfg, "  advisor: aiven-gateway/gpt-sol\n") {
		t.Errorf("config:\n%s", cfg)
	}
}

func TestOmpInitPrintsPriceTableTieringAndDoneOnStderr(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).ompPlugin()
	r := s.run("omp", "init")
	for _, want := range []string{"claude-sonnet ($3.00 in / $15.00 out per 1M tok)\n", "\nTiering, cheapest first.", "  aiven-model  ", "\nPick a cross-reviewer from a different vendor"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.stderr)
		}
	}
	if !strings.HasSuffix(r.stderr, "\nDone. Start the router with: aiven-llm omp\n") || strings.Contains(r.stderr, "not installed") {
		t.Errorf("stderr:\n%s", r.stderr)
	}
}

func TestOmpInitRendersWithoutPluginThenHints(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	r := s.run("omp", "init")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, s.ompPluginHint()+"\nDone.") {
		t.Errorf("stderr lacks the plugin hint before Done:\n%s", r.stderr)
	}
	if !strings.Contains(s.readProject("config.yml"), "  architect: aiven-gateway/") {
		t.Error("config not written")
	}
}

func (s *session) writeProject(rel, content string) {
	s.t.Helper()
	p := filepath.Join(s.ompDir(), rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

func TestOmpInitPreservesUserKeysAndSecondRunIsByteIdentical(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.writeProject("config.yml", "# mine\ntools:\n  approvalMode: write\n")
	s.run("omp", "init")
	first := s.readProject("config.yml")
	if !strings.Contains(first, "# mine\n") || !strings.Contains(first, "approvalMode: write") {
		t.Fatalf("user keys lost:\n%s", first)
	}
	s.run("omp", "init")
	if second := s.readProject("config.yml"); second != first {
		t.Errorf("second run differs:\n%s\n--- first\n%s", second, first)
	}
}

func TestOmpInitRefusesUnusableConfigAndWritesNothingElse(t *testing.T) {
	cases := []struct{ name, config, want string }{
		{"malformed YAML", "a: [unclosed\n", "is not valid YAML ("},
		{"sequence at top level", "- a\n- b\n", "must be a YAML mapping at the top level. Fix or move it, then rerun."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
			s.writeProject("config.yml", tc.config)
			r := s.run("omp", "init")
			path := filepath.Join(s.ompDir(), "config.yml")
			if r.code != 1 || !strings.Contains(r.stderr, "aiven-llm: "+path+" ") || !strings.Contains(r.stderr, tc.want) || strings.Contains(r.stderr, "Done.") {
				t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
			}
			if got := s.readProject("config.yml"); got != tc.config {
				t.Errorf("config rewritten: %q", got)
			}
			for _, rel := range []string{"gateway-router.state.json", "rules/gateway-router.md"} {
				if _, err := os.Stat(filepath.Join(s.ompDir(), rel)); err == nil {
					t.Errorf("wrote %s", rel)
				}
			}
		})
	}
}

func TestOmpInitWarnsOnStderr(t *testing.T) {
	cases := []struct{ name, config, state, want string }{
		{"project enabledModels", "enabledModels:\n  - anthropic/claude-sonnet-4-5\n", "", "overlay outranks"},
		{"edited advisor", "modelRoles:\n  advisor: my/own\n", `{"version":1,"managed":{"modelRoles.advisor":"aiven-gateway/claude-opus"}}`, "warning: left modelRoles.advisor alone: it is \"my/own\", not the \"aiven-gateway/claude-opus\" a previous init wrote.\n"},
		{"unreadable sidecar", "modelRoles:\n  advisor: my/own\n", "{garbage", "gateway-router.state.json is unreadable; treating every optional key as hand-written and leaving it alone.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
			s.writeProject("config.yml", tc.config)
			if tc.state != "" {
				s.writeProject("gateway-router.state.json", tc.state)
			}
			r := s.run("omp", "init")
			if r.code != 0 || !strings.Contains(r.stderr, "warning: ") || !strings.Contains(r.stderr, tc.want) {
				t.Fatalf("exit %d, stderr %q lacks %q", r.code, r.stderr, tc.want)
			}
			if strings.Contains(tc.name, "advisor") || strings.Contains(tc.name, "sidecar") {
				if cfg := s.readProject("config.yml"); !strings.Contains(cfg, "advisor: my/own\n") {
					t.Errorf("hand-edited advisor not kept:\n%s", cfg)
				}
			}
		})
	}
}

func TestOmpInitDefaultsFollowLaddersThenCheapestPinnable(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want [5]string // architect, builder, cross-reviewer, bulk-reader, boilerplate-writer
	}{
		{"ladder where granted, else cheapest", keyFable, [5]string{"claude-fable", "claude-sonnet", "claude-sonnet", "claude-sonnet", "claude-sonnet"}},
		{"priced beats unpriced", keyFastAndGLM, [5]string{"glm-fast", "glm-fast", "glm-fast", "glm-fast", "glm-fast"}},
		{"zero price counts as unpriced", keyZeroPrice, [5]string{"glm-fast", "glm-fast", "glm-fast", "glm-fast", "glm-fast"}},
		{"unpriced when nothing is priced, never the router", keyUnpriced, [5]string{"glm", "glm", "glm", "glm", "glm"}},
		{"equal prices pick first alias", keyTiedPrices, [5]string{"alpha", "alpha", "alpha", "alpha", "alpha"}},
		{"production ids", keyProduction, [5]string{"claude-opus-5-5", "claude-sonnet-5-5", "gpt-6-astra", "claude-haiku-5.5", "claude-haiku-5.5"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(tc.key)
			if r := s.run("omp", "init"); r.code != 0 {
				t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
			}
			cfg := s.readProject("config.yml")
			for i, role := range []string{"architect", "builder", "cross-reviewer", "bulk-reader", "boilerplate-writer"} {
				if want := "  " + role + ": aiven-gateway/" + tc.want[i] + "\n"; !strings.Contains(cfg, want) {
					t.Errorf("config lacks %q:\n%s", want, cfg)
				}
			}
		})
	}
}

func TestOmpInitFailsWhenKeyGrantsNothingPinnable(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyRouterOnly)
	r := s.run("omp", "init")
	if r.code != 1 || !strings.HasSuffix(r.stderr, "aiven-llm: your key grants no model that can be pinned to a role\n") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if _, err := os.Stat(s.ompDir()); err == nil {
		t.Error("wrote .omp")
	}
}

// scriptedPrompter answers ReadLine from a queue and records the prompts; an exhausted queue is end of input.
type scriptedPrompter struct {
	answers []string
	prompts []string
}

func (p *scriptedPrompter) ReadSecret(string) (string, error) { return "", io.EOF }

func (p *scriptedPrompter) ReadLine(prompt string) (string, error) {
	p.prompts = append(p.prompts, prompt)
	if len(p.answers) == 0 {
		return "", io.EOF
	}
	a := p.answers[0]
	p.answers = p.answers[1:]
	return a, nil
}

func TestOmpInitAsksEachRoleOnTerminalAndReasksUntilGranted(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	p := &scriptedPrompter{answers: []string{"nope", "gpt-sol", "", "claude-opus", "", "", ""}}
	s.deps.Prompter = p
	r := s.run("omp", "init")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	wantPrompts := []string{
		"architect [claude-opus]: ", "architect [claude-opus]: ", "builder [claude-sonnet]: ", "cross-reviewer [claude-opus]: ",
		"bulk-reader [gpt-sol]: ", "boilerplate-writer [gpt-sol]: ", "advisor [off]: ",
	}
	if !slices.Equal(p.prompts, wantPrompts) {
		t.Errorf("prompts %q, want %q", p.prompts, wantPrompts)
	}
	if !strings.Contains(r.stderr, "not an alias your key grants. Pick one of the aliases listed above.\n") {
		t.Errorf("stderr lacks re-ask notice:\n%s", r.stderr)
	}
	if cfg := s.readProject("config.yml"); !strings.Contains(cfg, "  architect: aiven-gateway/gpt-sol\n") || !strings.Contains(cfg, "  builder: aiven-gateway/claude-sonnet\n") {
		t.Errorf("config:\n%s", cfg)
	}
}

func TestOmpInitAsksOnlyForWhatFlagsLeaveOpen(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	p := &scriptedPrompter{answers: []string{"bogus", "main", ""}}
	s.deps.Prompter = p
	r := s.run("omp", "init", "--architect", "claude-opus", "--builder", "claude-sonnet", "--cross-reviewer", "gpt-sol", "--bulk-reader", "glm", "--boilerplate-writer", "glm")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if want := []string{"advisor [off]: ", "advisor [off]: ", "advisor model [gpt-sol]: "}; !slices.Equal(p.prompts, want) {
		t.Errorf("prompts %q, want %q", p.prompts, want)
	}
	for _, want := range []string{"advisor: a second model reviews transcript deltas every turn", "  both     watch each\n", "pick off, main, builder or both.\n"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr lacks %q", want)
		}
	}
	if cfg := s.readProject("config.yml"); !strings.Contains(cfg, "  advisor: aiven-gateway/gpt-sol\n") {
		t.Errorf("config:\n%s", cfg)
	}
}

func TestOmpInitTakesDefaultsWhenTerminalInputEnds(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.deps.Prompter = &scriptedPrompter{}
	if r := s.run("omp", "init"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if cfg := s.readProject("config.yml"); !strings.Contains(cfg, "  architect: aiven-gateway/claude-opus\n") || strings.Contains(cfg, "dvisor") {
		t.Errorf("config:\n%s", cfg)
	}
}

func TestOmpInitFailsBeforeGatewayWhenBinaryMissing(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	s.runner.missing = []string{"omp"}
	r := s.run("omp", "init")
	if want := "aiven-llm: omp is required but not installed\n"; r.code != 1 || r.stderr != want {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if n := len(g.journal()); n != 0 {
		t.Errorf("gateway got %d requests", n)
	}
	if _, err := os.Stat(s.ompDir()); err == nil {
		t.Error("wrote .omp")
	}
}

func TestOmpInitTreatsUnreadableSidecarAsEmptyWithWarning(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads mode 0000 files")
	}
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.writeProject("config.yml", "modelRoles:\n  advisor: my/own\n")
	s.writeProject("gateway-router.state.json", `{"version":1,"managed":{"modelRoles.advisor":"my/own"}}`)
	sidecar := filepath.Join(s.ompDir(), "gateway-router.state.json")
	if err := os.Chmod(sidecar, 0); err != nil {
		t.Fatal(err)
	}
	r := s.run("omp", "init")
	if want := sidecar + " is unreadable; treating every optional key as hand-written and leaving it alone.\n"; r.code != 0 || !strings.Contains(r.stderr, "warning: "+want) {
		t.Fatalf("exit %d, stderr %q lacks %q", r.code, r.stderr, want)
	}
	if cfg := s.readProject("config.yml"); !strings.Contains(cfg, "advisor: my/own\n") {
		t.Errorf("hand-written advisor not kept:\n%s", cfg)
	}
}

func TestOmpInitReasksWhenTerminalAnswerIsRouter(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	p := &scriptedPrompter{answers: []string{"aiven-model", "claude-sonnet", "", "", "", "", "main", "aiven-model", ""}}
	s.deps.Prompter = p
	r := s.run("omp", "init")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	wantPrompts := []string{
		"architect [claude-opus]: ", "architect [claude-opus]: ", "builder [claude-sonnet]: ", "cross-reviewer [claude-opus]: ",
		"bulk-reader [gpt-sol]: ", "boilerplate-writer [gpt-sol]: ", "advisor [off]: ", "advisor model [claude-opus]: ", "advisor model [claude-opus]: ",
	}
	if !slices.Equal(p.prompts, wantPrompts) {
		t.Errorf("prompts %q, want %q", p.prompts, wantPrompts)
	}
	if n := strings.Count(r.stderr, "aiven-model picks a model per request, so the role would not be pinned. Pick a model.\n"); n != 2 {
		t.Errorf("router hint shown %d times, want 2:\n%s", n, r.stderr)
	}
	if cfg := s.readProject("config.yml"); !strings.Contains(cfg, "  architect: aiven-gateway/claude-sonnet\n") || strings.Contains(cfg, "aiven-gateway/aiven-model") {
		t.Errorf("config:\n%s", cfg)
	}
}

func TestOmpInitPromptAcceptsSelectorForm(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	p := &scriptedPrompter{answers: []string{"aiven-gateway/claude-opus", "", "", "", "", ""}}
	s.deps.Prompter = p
	r := s.run("omp", "init")
	if r.code != 0 || strings.Contains(r.stderr, "not an alias") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if len(p.prompts) != 6 {
		t.Errorf("prompts %q, want one per role", p.prompts)
	}
}

func TestOmpInitKeepsExistingFileModes(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.writeProject("config.yml", "tools:\n  approvalMode: write\n")
	s.writeProject("rules/gateway-router.md", "old\n")
	for rel, mode := range map[string]os.FileMode{"config.yml": 0o600, "rules/gateway-router.md": 0o640} {
		if err := os.Chmod(filepath.Join(s.ompDir(), rel), mode); err != nil {
			t.Fatal(err)
		}
	}
	if r := s.run("omp", "init"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	for rel, want := range map[string]os.FileMode{"config.yml": 0o600, "rules/gateway-router.md": 0o640, "gateway-router.state.json": 0o644} {
		if fi, err := os.Stat(filepath.Join(s.ompDir(), rel)); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s mode = %v (%v), want %v", rel, fi.Mode().Perm(), err, want)
		}
	}
}

// A failed write restores what was already written, so `--advisor off` on retry still sees the launcher's own keys.
func TestOmpInitRollsBackWhenLaterWriteFails(t *testing.T) {
	for _, prior := range []string{"", "tools:\n  approvalMode: write\n"} {
		t.Run(fmt.Sprintf("prior config %t", prior != ""), func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
			if prior != "" {
				s.writeProject("config.yml", prior)
			}
			mkdir(t, filepath.Join(s.ompDir(), "gateway-router.state.json")) // a directory cannot be renamed over
			r := s.run("omp", "init")
			if r.code != 1 || r.stdout != "" {
				t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
			got, err := os.ReadFile(filepath.Join(s.ompDir(), "config.yml"))
			switch {
			case prior == "" && !errors.Is(err, fs.ErrNotExist):
				t.Errorf("config left behind: %q", got)
			case prior != "" && string(got) != prior:
				t.Errorf("config = %q, want %q", got, prior)
			}
		})
	}
}
