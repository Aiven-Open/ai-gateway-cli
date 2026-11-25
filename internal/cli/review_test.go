// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
)

// Show file changes prints every pending tool's full redacted diff once, then asks the same question again.
func TestReviewShowsFileChangesThenAsksAgain(t *testing.T) {
	for _, tools := range [][]string{{"pi"}, {"pi", "opencode"}} {
		sel := &scriptedSelector{answers: []selectAnswer{{picked: []int{1}}, {picked: []int{0}}}}
		var stderr strings.Builder
		deps := inlineDeps(sel, &stderr)
		p := newPresenter(config.Env{}, deps, false)
		var pending []pendingPreview
		for _, tool := range tools {
			files := []harness.File{{
				Path: filepath.Join(t.TempDir(), tool+".json"),
				Data: []byte(`{"models":["m1","m2","m3"],"apiKey":"SYNTHETIC_SECRET"}` + "\n"),
			}}
			pending = append(pending, pendingPreview{
				target: &setupTarget{out: &targetOutcome{tool: tool}},
				files:  files, write: files,
				names: backupNames{time.Unix(0, 0), map[string]string{}},
			})
		}
		approved, outcome := approveInteractive(deps, p, pending, false)
		if len(approved) != len(tools) || outcome != reviewApproved || len(sel.asked) != 2 {
			t.Fatalf(
				"%v: approved %d, outcome %v, questions %d",
				tools,
				len(approved),
				outcome,
				len(sel.asked),
			)
		}
		want := []string{"Apply", "Show file changes", "Cancel"}
		if len(tools) > 1 {
			want[0] = "Apply all"
		}
		var labels []string
		for _, o := range sel.asked[0].options {
			labels = append(labels, o.label)
		}
		out := stderr.String()
		if !slices.Equal(labels, want) || strings.Count(out, "+++ ") != len(tools) ||
			!strings.Contains(out, `"m3"`) || strings.Contains(out, "SYNTHETIC_SECRET") ||
			!strings.Contains(out, "Values that look like credentials are shown as redacted placeholders.") {
			t.Errorf("%v: options %q, stderr %q", tools, labels, out)
		}
	}
}
