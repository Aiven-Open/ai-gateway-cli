// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

// A skipped tool is reachable only without a terminal, where one tool is named and no other can be up to date, so this
// calls the end of a run directly.
func TestCommitNoWritesKeepsOldKeyWhenAnotherToolWasSkipped(t *testing.T) {
	env := config.Env{BaseURL: "https://a.example", UserHome: t.TempDir()}
	if _, err := creds.Write(env.UserHome, env.BaseURL, "sk-old", fsutil.WriteAtomic); err != nil {
		t.Fatal(err)
	}
	deps := Deps{Stderr: io.Discard}
	p := newPresenter(env, deps, true)
	s := &setupSession{env: env, deps: deps, key: "sk-new", p: p, run: &setupRun{
		env: env,
		p:   p,
		targets: []*targetOutcome{
			{tool: "claude", status: statusUnchanged},
			{tool: "codex", status: statusSkipped},
		},
	}}
	s.commitNoWrites()
	if got, err := storedKey(env); err != nil || got != "sk-old" {
		t.Errorf("stored key = %q, %v; want sk-old", got, err)
	}
	if !strings.HasPrefix(s.run.credential, "not saved by this run") {
		t.Errorf("credential = %q", s.run.credential)
	}
}
