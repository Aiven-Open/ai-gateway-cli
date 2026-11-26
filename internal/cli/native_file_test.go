// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// The prompt names the file the harness planner will edit, which may be opencode.jsonc or OMP's config.yaml.
// Mutant: nativeFileOf returns the first snapshot file whatever the launcher-only plan holds.
func TestDefaultProviderPromptNamesInspectedFile(t *testing.T) {
	for _, tc := range []struct{ tool, file, data string }{
		{"opencode", ".config/opencode/opencode.jsonc", "{}\n"},
		{"omp", ".omp/agent/config.yaml", "theme: dark\n"},
	} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
		writeFile(t, filepath.Join(s.fakeHome, filepath.FromSlash(tc.file)), tc.data)
		s.deps.Prompter = &scriptedPrompter{answers: []string{"", "n", "n"}}
		r := s.run("setup", tc.tool, "--plain")
		if !strings.Contains(r.stderr, "updates ~/"+tc.file) {
			t.Errorf("%s: prompt does not name ~/%s: %q", tc.tool, tc.file, r.stderr)
		}
	}
}
