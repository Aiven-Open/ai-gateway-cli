// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
)

// Enter on a rerun keeps the launcher model saved earlier instead of switching to the first preset.
// Mutant: inspectConfiguredModel returns an empty configuredModel for launcher tools.
func TestSingleToolRerunKeepsSavedLauncherModel(t *testing.T) {
	for _, tool := range []string{"pi", "opencode", "omp"} {
		t.Run(tool, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyProduction)
			if r := s.run("setup", tool, "--yes"); r.code != 0 {
				t.Fatalf("setup: %s", r.stderr)
			}
			prefs := map[string]string{tool: "deepseek-v3.2"}
			if err := config.SaveModelPrefs(s.fakeHome, s.env.BaseURL, prefs, nil); err != nil {
				t.Fatal(err)
			}
			// Enter on the model menu, n on the default provider, 1 at review if anything changed.
			s.deps.Prompter = &scriptedPrompter{answers: []string{"", "n", "1"}}
			r := s.run("setup", tool, "--plain")
			got, err := config.LoadModelPref(s.fakeHome, s.env.BaseURL, tool)
			if r.code != 0 || err != nil || got != "deepseek-v3.2" ||
				!strings.Contains(r.stderr, "Keep current (deepseek-v3.2)") {
				t.Fatalf("exit %d, preference %q %v, stderr %q", r.code, got, err, r.stderr)
			}
		})
	}
}
