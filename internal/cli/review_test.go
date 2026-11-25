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
		labels := optionLabels(sel.asked[0])
		// The question after Show file changes offers the same options again.
		again := optionLabels(sel.asked[1])
		out := stderr.String()
		if !slices.Equal(labels, want) || !slices.Equal(again, want) ||
			strings.Count(out, "+++ ") != len(tools) ||
			!strings.Contains(out, `"m3"`) ||
			strings.Contains(out, "SYNTHETIC_SECRET") ||
			!strings.Contains(out, "Values that look like credentials are shown as redacted placeholders.") {
			t.Errorf("%v: options %q then %q, stderr %q", tools, labels, again, out)
		}
	}
}

// Show file changes can be pressed any number of times; only Apply, Back or Cancel ends the question.
func TestReviewKeepsShowingFileChangesPressAfterPress(t *testing.T) {
	const presses = 12
	var answers []selectAnswer
	for range presses {
		answers = append(answers, selectAnswer{picked: []int{1}})
	}
	sel := &scriptedSelector{answers: append(answers, selectAnswer{picked: []int{0}})}
	var stderr strings.Builder
	deps := inlineDeps(sel, &stderr)
	p := newPresenter(config.Env{}, deps, false)
	files := []harness.File{{Path: filepath.Join(t.TempDir(), "pi.json"), Data: []byte("{}\n")}}
	pending := []pendingPreview{{
		target: &setupTarget{out: &targetOutcome{tool: "pi"}},
		files:  files, write: files,
		names: backupNames{time.Unix(0, 0), map[string]string{}},
	}}
	approved, outcome := approveInteractive(deps, p, pending, false)
	if len(approved) != 1 || outcome != reviewApproved || len(sel.asked) != presses+1 ||
		strings.Contains(stderr.String(), "no answer") {
		t.Fatalf("approved %d, outcome %v, questions %d, stderr %q",
			len(approved), outcome, len(sel.asked), stderr.String())
	}
}
