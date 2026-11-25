// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"io"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
)

// A custom model's label is its raw catalogue id; the inline list renders details as they are.
func TestCustomizeHubSanitizesDraftLabels(t *testing.T) {
	sel := &scriptedSelector{}
	deps := inlineDeps(sel, io.Discard)
	p := newPresenter(config.Env{}, deps, false)
	eligible := []*setupTarget{
		{out: &targetOutcome{tool: "pi"}},
		{out: &targetOutcome{tool: "opencode"}},
	}
	draft := map[string]toolDraft{"pi": {
		selection: harness.ModelSelection{Model: "evil\x1b[31m‮model"},
		label:     "evil\x1b[31m‮model",
		set:       true,
	}}
	_, _ = selectCustomizeHub(deps, p, config.Env{}, eligible, nil, draft)
	if len(sel.asked) == 0 {
		t.Fatal("hub asked nothing")
	}
	if got, want := sel.asked[0].options[0].detail, safe("evil\x1b[31m‮model"); got != want {
		t.Errorf("hub detail %q, want %q", got, want)
	}
}
