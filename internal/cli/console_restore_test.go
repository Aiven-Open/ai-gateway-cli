// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"io"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
)

type handoffRunner struct{ onExec func() }

func (handoffRunner) LookPath(name string) (string, error) { return "/usr/bin/" + name, nil }

func (r handoffRunner) Exec(string, []string, []string) error {
	r.onExec()
	return nil
}

// Windows Exec waits for the harness and exits without returning, so no deferred restore runs after it.
func TestLaunchRestoresConsoleBeforeHandoff(t *testing.T) {
	restored, restoredAtExec := false, false
	registerVTRestore(func() { restored = true })
	deps := Deps{
		Stderr: io.Discard,
		Runner: handoffRunner{onExec: func() { restoredAtExec = restored }},
	}
	plan := harness.Plan{Argv: []string{"codex"}}
	if code := launch(plan, config.Env{}, deps, newPresenter(config.Env{}, deps, true)); code != 0 ||
		!restoredAtExec {
		t.Fatalf("exit %d, console restored before handoff %v", code, restoredAtExec)
	}
}
