// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"os"
	"path/filepath"
	"testing"
)

// An OMP config.yml whose modelRoles is a YAML alias cannot be edited, which matters only when Aiven is to become
// the default: launcher-only setup proceeds and leaves the file alone.
// Mutant: remove the launcher-only fallback prepare call in prepareSnapshot.
func TestLauncherOnlySetupIgnoresUneditableNativeDefault(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	config := filepath.Join(s.fakeHome, ".omp/agent/config.yml")
	const aliased = "roles: &roles\n  default: other/model\nmodelRoles: *roles\n"
	writeFile(t, config, aliased)
	if r := s.run("setup", "omp", "--yes"); r.code != 0 {
		t.Fatalf("launcher-only: exit %d, stderr %q", r.code, r.stderr)
	}
	if got := readOrEmpty(t, config); got != aliased {
		t.Errorf("config.yml changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(s.fakeHome, ".omp/agent/models.yml")); err != nil {
		t.Errorf("models.yml not written: %v", err)
	}
}
