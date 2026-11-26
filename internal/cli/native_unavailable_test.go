// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// When the native default file cannot be edited, the default prompt does not offer Yes and names no file.
// Mutant: selectDirectInvocation offers every eligible tool whether or not its native default is editable.
func TestDefaultPromptWithholdsYesWhenNativeFileUneditable(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	config := filepath.Join(s.fakeHome, ".omp/agent/config.yml")
	const aliased = "roles: &roles\n  default: other/model\nmodelRoles: *roles\n"
	writeFile(t, config, aliased)
	s.deps.Prompter = &scriptedPrompter{answers: []string{"", "", "1"}}
	r := s.run("setup", "omp", "--plain")
	const why = "OMP default config cannot be edited because modelRoles must be a direct YAML mapping, so Aiven is available through the launch command only."
	if r.code != 0 || !strings.Contains(r.stderr, why) ||
		strings.Contains(r.stderr, "Yes — make Aiven the default") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if got := readOrEmpty(t, config); got != aliased {
		t.Errorf("config.yml changed: %q", got)
	}
}
