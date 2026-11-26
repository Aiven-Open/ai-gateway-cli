// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"path/filepath"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
)

// Mutant: FindPreset takes the first alias of the whole catalogue, or sonnet from unfiltered models.
func TestClaudeDefaultFollowsCatalogueOwnership(t *testing.T) {
	for key, want := range map[string]string{
		keyOwnedFable:    "claude-fable-5-2",
		keyPrivateSonnet: "claude-haiku-5-5",
		// An owner no reviewed list names keeps its Claude id. Mutant: IsClaude refuses unreviewed owners.
		keyUnreviewedOwner: "claude-sonnet-5-5",
	} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(key)
		if r := s.run("setup", "claude", "--yes"); r.code != 0 {
			t.Fatalf("%s: exit %d, stderr %q", key, r.code, r.stderr)
		}
		path := filepath.Join(s.fakeHome, filepath.FromSlash(claude.SettingsFile))
		got, _ := readJSON(t, path)["model"].(string)
		if got != want {
			t.Errorf("%s: default model %q, want %q", key, got, want)
		}
	}
}
