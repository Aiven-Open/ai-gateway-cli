// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Aiven-Open/ai-gateway-cli/internal/cli"
	"github.com/Aiven-Open/ai-gateway-cli/internal/config"
	"github.com/Aiven-Open/ai-gateway-cli/internal/keystore"
)

// Keys of the fake gateway; bodies follow the gateway's `/v1/models` shape.
const (
	keyValid      = "sk-test-valid"
	keyEmpty      = "sk-test-empty"
	keyQuote      = "sk-test-quote"
	keyClaudeOnly = "sk-test-claude-only"
	keyFlash      = "sk-test-flash"
	keyRouterOnly = "sk-test-router-only"
	keyRouterSol  = "sk-test-router-sol"
	keyMidContext = "sk-test-mid-context"
	keyFable      = "sk-test-fable"
	keyAllTiers   = "sk-test-all-tiers"
	keyFastAndGLM = "sk-test-fast-and-glm"
	keyUnpriced   = "sk-test-unpriced-only"
	keyTiedPrices = "sk-test-tied-prices"
	keyZeroPrice  = "sk-test-zero-price"
	keyProduction = "sk-test-production"
	// Production ids minus each ladder's first choice.
	keyProductionFallback = "sk-test-production-fallback"
	// ai.aiven.io names beside the POC gateway's and older aliases for the same models.
	keyMixedNames = "sk-test-mixed-names"
)

// productionEntries are ids https://ai.aiven.io serves (2026-10-26), without context or prices as it lists them.
var productionEntries = bareEntries("claude-haiku-4-5", "claude-haiku-5.5", "claude-opus-5-5", "claude-sonnet-5-5", "deepseek-v3.2", "glm-4.7-flash", "glm-5", "gpt-5.4", "gpt-6-astra", "gpt-6-sol", "gpt-6.1-sol")

func bareEntries(ids ...string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = `{"id":"` + id + `","object":"model","created":0,"owned_by":"x","input_usd_per_million_tokens":null,"output_usd_per_million_tokens":null}`
	}
	return out
}

// aiven-model is the router: zero price, 1M context, listed first as the Frontend does.
const (
	routerEntry = `{"id":"aiven-model","object":"model","created":0,"owned_by":"Aiven","context_window_tokens":1000000,"input_usd_per_million_tokens":0,"output_usd_per_million_tokens":0}`
	sonnetEntry = `{"id":"claude-sonnet","object":"model","created":0,"owned_by":"anthropic","context_window_tokens":1000000,"input_usd_per_million_tokens":3,"output_usd_per_million_tokens":15}`
	opusEntry   = `{"id":"claude-opus","object":"model","created":0,"owned_by":"anthropic","context_window_tokens":1000000,"input_usd_per_million_tokens":15,"output_usd_per_million_tokens":75}`
	haikuEntry  = `{"id":"claude-haiku","object":"model","created":0,"owned_by":"anthropic","context_window_tokens":200000,"input_usd_per_million_tokens":1,"output_usd_per_million_tokens":5}`
	flashEntry  = `{"id":"deepseek-flash","object":"model","created":0,"owned_by":"deepseek","context_window_tokens":1000000,"input_usd_per_million_tokens":0.15,"output_usd_per_million_tokens":0.6}`
	fableEntry  = `{"id":"claude-fable","object":"model","created":0,"owned_by":"anthropic","context_window_tokens":1000000,"input_usd_per_million_tokens":10,"output_usd_per_million_tokens":50}`
	// Above Claude's 200K default but under 1M, as the cheap tiers are.
	midEntry = `{"id":"glm-fast","object":"model","created":0,"owned_by":"zai","context_window_tokens":262144,"input_usd_per_million_tokens":0.5,"output_usd_per_million_tokens":2}`
	// No context field at all.
	solEntry = `{"id":"gpt-sol","object":"model","created":0,"owned_by":"openai","input_usd_per_million_tokens":1.25,"output_usd_per_million_tokens":10}`
	// Nulls for what the catalogue does not price.
	glmEntry = `{"id":"glm","object":"model","created":0,"owned_by":"zai","context_window_tokens":1310720,"input_usd_per_million_tokens":null,"output_usd_per_million_tokens":null}`
	// Equal output prices, so only alias order tells them apart.
	betaEntry  = `{"id":"beta","object":"model","created":0,"owned_by":"x","context_window_tokens":100000,"input_usd_per_million_tokens":1,"output_usd_per_million_tokens":5}`
	alphaEntry = `{"id":"alpha","object":"model","created":0,"owned_by":"x","context_window_tokens":100000,"input_usd_per_million_tokens":2,"output_usd_per_million_tokens":5}`
	// A zero price means unknown, not free.
	freeEntry  = `{"id":"aaa-free","object":"model","created":0,"owned_by":"x","context_window_tokens":100000,"input_usd_per_million_tokens":0,"output_usd_per_million_tokens":0}`
	quoteEntry = `{"id":"bad\"alias","object":"model","created":0,"owned_by":"x"}`
)

var keyModels = map[string][]string{
	keyValid:              {routerEntry, sonnetEntry, opusEntry, solEntry, glmEntry},
	keyEmpty:              {},
	keyQuote:              {quoteEntry},
	keyClaudeOnly:         {opusEntry, sonnetEntry, haikuEntry},
	keyFlash:              {routerEntry, opusEntry, flashEntry},
	keyRouterOnly:         {routerEntry},
	keyRouterSol:          {routerEntry, solEntry},
	keyMidContext:         {midEntry},
	keyFable:              {fableEntry, sonnetEntry},
	keyAllTiers:           {glmEntry, midEntry, flashEntry},
	keyFastAndGLM:         {glmEntry, midEntry},
	keyUnpriced:           {routerEntry, glmEntry},
	keyTiedPrices:         {betaEntry, alphaEntry},
	keyZeroPrice:          {freeEntry, midEntry},
	keyProduction:         productionEntries,
	keyProductionFallback: bareEntries("claude-haiku-4-5", "claude-opus-5-5", "claude-sonnet-5-5", "gpt-5.4", "gpt-6-sol"),
	keyMixedNames:         bareEntries("claude-haiku-5-5", "claude-haiku-5.5", "claude-sonnet", "claude-sonnet-5-5"),
}

// gatewayDouble is an httptest AI Gateway serving GET /v1/models and journalling requests.
type gatewayDouble struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*http.Request
}

func newGatewayDouble(t *testing.T) *gatewayDouble {
	t.Helper()
	g := &gatewayDouble{}
	g.Server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.Close)
	return g
}

func (g *gatewayDouble) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.requests = append(g.requests, r)
	g.mu.Unlock()
	key, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	entries, ok := keyModels[key]
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Authentication Error"}}`))
		return
	}
	if r.URL.Path != "/v1/models" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"object":"list","data":[` + strings.Join(entries, ",") + `]}`))
}

func (g *gatewayDouble) journal() []*http.Request {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]*http.Request(nil), g.requests...)
}

// execCall is one captured Runner.Exec.
type execCall struct {
	name      string
	argv, env []string
}

// envVar returns the last value of name in the child environment.
func (c execCall) envVar(name string) (string, bool) {
	val, found := "", false
	for _, kv := range c.env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			val, found = v, true
		}
	}
	return val, found
}

// fakeRunner records exec requests instead of replacing the test process; every binary is installed except missing.
type fakeRunner struct {
	calls   []execCall
	missing []string
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if slices.Contains(f.missing, name) {
		return "", errors.New(name + " is required but not installed")
	}
	return "/usr/bin/" + name, nil
}

func (f *fakeRunner) Exec(name string, argv, env []string) error {
	f.calls = append(f.calls, execCall{name, argv, env})
	return nil
}

// session is one launcher invocation context: a temp home, the double's URL, captured streams.
type session struct {
	t        *testing.T
	env      config.Env
	deps     cli.Deps
	out      *bytes.Buffer
	errb     *bytes.Buffer
	store    keystore.Store
	runner   *fakeRunner
	fakeHome string
}

func newSession(t *testing.T, g *gatewayDouble) *session {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	fakeHome := filepath.Join(root, "fakehome")
	project := filepath.Join(root, "project")
	for _, d := range []string{fakeHome, project} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	s := &session{
		t: t,
		env: config.Env{
			BaseURL: g.URL, Home: home, WorkDir: project, OMPHome: filepath.Join(fakeHome, ".omp"), PiAgentDir: filepath.Join(fakeHome, ".pi", "agent"), CodexHome: filepath.Join(fakeHome, ".codex"),
			Environ: []string{"PATH=/usr/bin:/bin", "HOME=" + fakeHome},
		},
		out:      &bytes.Buffer{},
		errb:     &bytes.Buffer{},
		store:    keystore.NewFile(home),
		runner:   &fakeRunner{},
		fakeHome: fakeHome,
	}
	s.deps = cli.Deps{Stdin: strings.NewReader(""), Stdout: s.out, Stderr: s.errb, Keystore: s.store, Runner: s.runner}
	return s
}

func (s *session) stdin(text string) *session {
	s.deps.Stdin = strings.NewReader(text)
	return s
}

func (s *session) run(args ...string) result {
	s.t.Helper()
	s.out.Reset()
	s.errb.Reset()
	code := cli.Run(context.Background(), args, s.env, s.deps)
	return result{code, s.out.String(), s.errb.String()}
}

// loggedIn stores key as `login` would, without going through the launcher.
func (s *session) loggedIn(key string) *session {
	s.t.Helper()
	rec := keystore.Record{BaseURL: s.env.BaseURL, Key: key}
	if _, err := s.store.Set(rec); err != nil {
		s.t.Fatal(err)
	}
	return s
}

// keyFiles lists every file under the home, relative.
func (s *session) homeFiles() []string {
	var files []string
	_ = filepath.WalkDir(s.env.Home, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(s.env.Home, p)
			files = append(files, rel)
		}
		return nil
	})
	return files
}
