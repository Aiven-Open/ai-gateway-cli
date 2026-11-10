// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// toolFiles are the files whose presence shows setup configured each tool.
var toolFiles = map[string]string{
	"pi": ".pi/agent/models.json", "opencode": ".config/opencode/opencode.json", "codex": ".codex/config.toml",
	"claude": ".claude/aiven.settings.json", "omp": ".omp/agent/models.yml",
}

func TestLaunchPickerRequiresOneInstalledHarnessAndCancelHasNoEffects(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, selected, tail string
		answers              []string
		missing              []string
		code                 int
	}{
		{name: "number", selected: "pi", answers: []string{"1"}},
		{name: "name", selected: "codex", answers: []string{"codex"}},
		{name: "multiple then single", selected: "pi", answers: []string{"pi,codex", "pi"}},
		{name: "uninstalled then single", selected: "pi", missing: []string{"codex"}, answers: []string{"codex", "pi"}},
		{name: "cancel", answers: []string{""}, tail: "\nNothing launched.\n"},
		{name: "EOF", code: 1},
		{name: "none installed", missing: []string{"pi", "opencode", "codex", "claude", "omp"}, code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := newGatewayDouble(t)
			s := newSession(t, g).stdin(keyValid)
			if tc.selected != "" {
				s.loggedIn(keyValid).configured(tc.selected)
			}
			s.runner.missing = tc.missing
			s.deps.Prompter = &scriptedPrompter{answers: tc.answers}
			before := len(g.journal())
			r := s.run("launch")
			if r.code != tc.code {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
			if !strings.HasSuffix(r.stderr, tc.tail) {
				t.Errorf("stderr %q does not end with %q", r.stderr, tc.tail)
			}
			if tc.selected == "" {
				if len(g.journal()) != before || len(s.runner.calls) != 0 {
					t.Fatal("cancel or invalid picker accessed gateway or executed a harness")
				}
				if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
					t.Fatal("picker saved a credential before selecting a harness")
				}
			} else if len(s.runner.calls) != 1 || s.runner.calls[0].argv[0] != tc.selected {
				t.Fatalf("wrong selection: %v", s.runner.calls)
			}
		})
	}
}

func TestSetupPickerKeepsGoingPastAToolWithMalformedConfig(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	writeFile(t, filepath.Join(s.fakeHome, toolFiles["pi"]), "{bad")
	s.deps.Prompter = &scriptedPrompter{answers: []string{"pi,codex", "", "", ""}}
	r := s.run("setup", "--key-stdin")
	if r.code != 1 || !strings.Contains(r.stderr, "aiven-ai-gateway: pi: ") || !strings.Contains(r.stderr, "written: "+filepath.Join(s.fakeHome, toolFiles["codex"])) || !strings.Contains(r.stderr, "Pi          failed") {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}

func TestSetupPickerOffersToolsOnPathAndConfiguresTheChosenOnes(t *testing.T) {
	const later = "To configure a harness later, run: aiven-ai-gateway setup <pi|opencode|codex|claude|omp>\n"
	for _, tc := range []struct {
		name     string
		missing  []string
		answers  []string
		menu     string
		want     []string
		code     int
		hint     string
		reprompt int
	}{
		{name: "number from detected", missing: []string{"pi", "opencode", "omp"}, answers: []string{"2", "", ""}, menu: "(found on PATH):\n  1) Codex (codex)\n  2) Claude Code (claude)\n", want: []string{"claude"}},
		{name: "names and numbers deduplicated", missing: []string{"omp"}, answers: []string{" 1, pi codex ", "", "", "", ""}, want: []string{"pi", "codex"}},
		{name: "all when none found gives installation guidance", missing: []string{"pi", "opencode", "codex", "claude", "omp"}, answers: []string{"ALL"}, menu: "(none found on PATH):\n  1) Pi (pi)\n  2) OpenCode (opencode)\n  3) Codex (codex)\n  4) Claude Code (claude)\n  5) OMP (omp)\n", code: 1},
		{name: "enter skips", answers: []string{""}, hint: later},
		{name: "s skips", answers: []string{"s"}, hint: later},
		{name: "re-asks then accepts unavailable tool", missing: []string{"omp"}, answers: []string{"9", "omp"}, code: 1, reprompt: 1},
		{name: "gives up after three", answers: []string{"x", "0", "pi,nope"}, code: 1, reprompt: 3},
		{name: "end of input", code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
			s.runner.missing = tc.missing
			p := &scriptedPrompter{answers: tc.answers}
			s.deps.Prompter = p
			r := s.run("setup", "--key-stdin")
			if r.code != tc.code || !strings.Contains(r.stderr, tc.menu) || strings.Count(r.stderr, "Choose from the list") != tc.reprompt || r.stdout != "" || !strings.Contains(r.stderr, tc.hint) {
				t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
			for tool, f := range toolFiles {
				_, err := os.Stat(filepath.Join(s.fakeHome, f))
				if configured := err == nil; configured != strings.Contains(" "+strings.Join(tc.want, " ")+" ", " "+tool+" ") {
					t.Errorf("%s configured = %v, want %v", tool, configured, !configured)
				}
			}
			if len(s.runner.calls) != 0 {
				t.Errorf("launched %v", s.runner.calls)
			}
		})
	}
}

func TestSetupPickerSkipsAToolWhoseConfigIsMoved(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	s.env.Environ = append(s.env.Environ, "CODEX_HOME="+filepath.Join(s.fakeHome, "elsewhere"))
	s.deps.Prompter = &scriptedPrompter{answers: []string{"pi,codex", "", ""}}
	r := s.run("setup", "--key-stdin")
	if r.code != 1 || !strings.Contains(r.stderr, "aiven-ai-gateway: codex: CODEX_HOME is set") || !strings.Contains(r.stderr, "Pi          configured\n") || !strings.Contains(r.stderr, "Codex       failed; CODEX_HOME is set") {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if _, err := os.Stat(filepath.Join(s.fakeHome, toolFiles["codex"])); err == nil {
		t.Error("codex configured despite CODEX_HOME")
	}
}

func TestSetupWithoutTerminalRequiresAHarnessBeforeSavingTheKey(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	r := s.run("setup", "--yes")
	if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "a harness is required without a terminal") {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
		t.Fatal("setup without a target saved a key")
	}
	for tool, f := range toolFiles {
		if _, err := os.Stat(filepath.Join(s.fakeHome, f)); err == nil {
			t.Errorf("%s configured without a terminal", tool)
		}
	}
}
