// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

const changedKeyCatalogue = `[{"id":"claude-sonnet-5-5","owned_by":"Anthropic"}]`

// hookingSelector runs onAsk before it answers the nth question, counted from one.
type hookingSelector struct {
	scriptedSelector
	nth   int
	onAsk func()
}

func (h *hookingSelector) Select(c choice) ([]int, error) {
	if len(h.asked)+1 == h.nth {
		h.onAsk()
	}
	return h.scriptedSelector.Select(c)
}

// hookedGateway serves the catalogue and runs onProbe on the first check request once armed is set.
func hookedGateway(t *testing.T, armed *atomic.Bool, onProbe func()) *httptest.Server {
	t.Helper()
	var once sync.Once
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"object":"list","data":` + changedKeyCatalogue + `}`))
			return
		}
		if armed != nil && armed.Load() {
			once.Do(onProbe)
		}
		_, _ = w.Write([]byte(`{"id":"synthetic","content":[],"choices":[],"output":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSetupLeavesSavedKeyAloneWhenItChangedAtApplyPrompt(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(path string)
		want   string
	}{
		"logout":          {func(path string) { _ = os.Remove(path) }, ""},
		"different login": {func(path string) { _ = fsutil.WriteAtomic(path, []byte("sk-other\n"), 0o600) }, "sk-other\n"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := hookedGateway(t, nil, nil)
			env, home := inlineSetupEnv(t, srv.URL)
			if _, err := creds.Write(env.UserHome, env.BaseURL, "sk-saved", fsutil.WriteAtomic); err != nil {
				t.Fatal(err)
			}
			path := creds.Path(env.UserHome, env.BaseURL)
			sel := &hookingSelector{
				nth:   3,
				onAsk: func() { tc.change(path) },
				scriptedSelector: scriptedSelector{
					answers: []selectAnswer{{picked: []int{0}}, {picked: []int{0}}, {picked: []int{0}}},
				},
			}
			var stderr strings.Builder
			deps := inlineDeps(sel, &stderr)
			deps.HTTP = srv.Client()
			code := Run(t.Context(), []string{"setup", "claude"}, env, deps)
			got := ""
			if data, err := os.ReadFile(path); err == nil {
				got = string(data)
			}
			out := stderr.String()
			if code == 0 || got != tc.want ||
				!strings.Contains(out, "the saved key changed while setup was running; no key was written") {
				t.Errorf("exit %d, key file %q, want %q:\n%s", code, got, tc.want, out)
			}
			if _, err := os.Stat(home + "/.claude"); err == nil {
				t.Errorf("Claude settings were written after the key changed")
			}
			if strings.Contains(out, "aiven-ai-gateway claude") ||
				strings.Contains(out, "aiven-ai-gateway launch") {
				t.Errorf("a launch command was offered:\n%s", out)
			}
		})
	}
}

// Claude is already configured, so nothing is written and the key file is the only thing left to decide.
func TestSetupNoWritesLeavesSavedKeyAloneWhenItWasRemoved(t *testing.T) {
	var path string
	var armed atomic.Bool
	srv := hookedGateway(t, &armed, func() { _ = os.Remove(path) })
	env, _ := inlineSetupEnv(t, srv.URL)
	path = creds.Path(env.UserHome, env.BaseURL)
	if _, err := creds.Write(env.UserHome, env.BaseURL, "sk-saved", fsutil.WriteAtomic); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	deps := inlineDeps(&scriptedSelector{}, &stderr)
	deps.HTTP = srv.Client()
	if code := Run(t.Context(), []string{"setup", "claude", "--yes"}, env, deps); code != 0 {
		t.Fatalf("first setup: exit %d:\n%s", code, stderr.String())
	}
	armed.Store(true)
	stderr.Reset()
	code := Run(t.Context(), []string{"setup", "claude", "--yes"}, env, deps)
	_, statErr := os.Stat(path)
	out := stderr.String()
	if code == 0 || !os.IsNotExist(statErr) ||
		!strings.Contains(out, "the saved key changed while setup was running; no key was written") ||
		strings.Contains(out, "aiven-ai-gateway claude") {
		t.Errorf("exit %d, stat %v:\n%s", code, statErr, out)
	}
}
