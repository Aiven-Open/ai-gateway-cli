// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"io"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
)

// Inline lists draw their own text, so the presenter's selector rewrites hints before drawing.
func TestInlineHintsUseInvokedCommand(t *testing.T) {
	sel := &scriptedSelector{answers: []selectAnswer{{picked: []int{0}}}}
	deps := inlineDeps(sel, io.Discard)
	deps.Command = shortName
	p := newPresenter(config.Env{}, deps, false)
	c := choice{
		title: "Default provider for Claude Code",
		options: []option{
			{label: "No — only via 'aiven-ai-gateway launch claude'", detail: "aiven-ai-gateway setup"},
		},
	}
	if _, err := p.inline.Select(c); err != nil {
		t.Fatal(err)
	}
	got := sel.asked[0].options[0]
	if got.label != "No — only via 'avn-aig launch claude'" || got.detail != "avn-aig setup" {
		t.Errorf("option %+v", got)
	}
	if c.options[0].label != "No — only via 'aiven-ai-gateway launch claude'" {
		t.Error("the caller's choice was changed")
	}
}
