// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/cli"
	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
	"github.com/aiven-open/ai-gateway-cli/internal/gateway"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/codex"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/omp"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/opencode"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/pi"
)

// Keys of the fake gateway and the `/v1/models` entries each one is granted.
const (
	keyValid      = "sk-test-valid"
	keyEmpty      = "sk-test-empty"
	keyClaudeOnly = "sk-test-claude-only"
	keyFlash      = "sk-test-flash"
	keyRouterOnly = "sk-test-router-only"
	keyRouterSol  = "sk-test-router-sol"
	keyMidContext = "sk-test-mid-context"
	keyFable      = "sk-test-fable"
	keyProduction = "sk-test-production"
	// Production ids minus each ladder's first choice.
	keyProductionFallback = "sk-test-production-fallback"
	// ai.aiven.io names beside historical aliases for the same models.
	keyMixedNames = "sk-test-mixed-names"
	keyMaxInput   = "sk-test-max-input"
	keyShort      = "sk-short"
	keyBare       = "bare-key-000"
	keyLong       = "AVNS_test.fixture00000000wxyz"
	keyNoInfer    = "sk-test-no-inference"
	// Grants an alias holding a double quote.
	keyQuoted = "sk-test-quoted"
)

// fixedNow is every session's first clock reading; each later one is a second on, as a real clock would be, so the
// runs of one session name distinct backups.
var fixedNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// productionEntries are bare model entries that omit optional context window and price fields.
var productionEntries = bareEntries("claude-haiku-4-5", "claude-haiku-5.5", "claude-opus-5-5", "claude-sonnet-5-5", "deepseek-v3.2", "glm-4.7-flash", "glm-5", "gpt-5.4", "gpt-6-astra", "gpt-6-sol", "gpt-6.1-sol")

func bareEntries(ids ...string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = `{"id":"` + id + `","object":"model","created":0,"owned_by":"x","input_usd_per_million_tokens":null,"output_usd_per_million_tokens":null}`
	}
	return out
}

// maxInputEntries carry `max_input_tokens` without `context_window_tokens` or prices.
var maxInputEntries = []string{
	`{"id":"claude-sonnet-5-5","object":"model","created":0,"owned_by":"x","max_input_tokens":1000000}`,
	`{"id":"deepseek-v3.2","object":"model","created":0,"owned_by":"x","max_input_tokens":163840}`,
	`{"id":"claude-haiku-5.5","object":"model","created":0,"owned_by":"x","max_input_tokens":200000}`,
	`{"id":"glm-4.7-flash","object":"model","created":0,"owned_by":"x","max_input_tokens":200000}`,
	`{"id":"claude-opus-4-8","object":"model","created":0,"owned_by":"x","max_input_tokens":500000}`,
}

// aiven-model is the router: zero price, 1M context, listed first as the gateway lists it.
const (
	routerEntry = `{"id":"aiven-model","object":"model","created":0,"owned_by":"Aiven","context_window_tokens":1000000,"input_usd_per_million_tokens":0,"output_usd_per_million_tokens":0}`
	sonnetEntry = `{"id":"claude-sonnet","object":"model","created":0,"owned_by":"anthropic","context_window_tokens":1000000,"max_output_tokens":64000,"input_usd_per_million_tokens":3,"output_usd_per_million_tokens":15}`
	opusEntry   = `{"id":"claude-opus","object":"model","created":0,"owned_by":"anthropic","display_name":"Claude Opus","context_window_tokens":1000000,"input_usd_per_million_tokens":15,"output_usd_per_million_tokens":75}`
	haikuEntry  = `{"id":"claude-haiku","object":"model","created":0,"owned_by":"anthropic","context_window_tokens":200000,"input_usd_per_million_tokens":1,"output_usd_per_million_tokens":5}`
	flashEntry  = `{"id":"deepseek-flash","object":"model","created":0,"owned_by":"deepseek","context_window_tokens":1000000,"input_usd_per_million_tokens":0.15,"output_usd_per_million_tokens":0.6}`
	fableEntry  = `{"id":"claude-opus-5-5","object":"model","created":0,"owned_by":"anthropic","context_window_tokens":1000000,"input_usd_per_million_tokens":10,"output_usd_per_million_tokens":50}`
	// Above Claude's 200K default but under 1M, as the cheap tiers are.
	midEntry = `{"id":"glm-fast","object":"model","created":0,"owned_by":"zai","context_window_tokens":262144,"input_usd_per_million_tokens":0.5,"output_usd_per_million_tokens":2}`
	// No context field at all.
	solEntry = `{"id":"gpt-sol","object":"model","created":0,"owned_by":"OpenAI","input_usd_per_million_tokens":1.25,"output_usd_per_million_tokens":10}`
	// Nulls for what the catalogue does not price.
	glmEntry = `{"id":"glm","object":"model","created":0,"owned_by":"zai","context_window_tokens":1310720,"input_usd_per_million_tokens":null,"output_usd_per_million_tokens":null}`
)

var keyModels = map[string][]string{
	keyValid:              {routerEntry, sonnetEntry, opusEntry, solEntry, glmEntry},
	keyEmpty:              {},
	keyClaudeOnly:         {opusEntry, sonnetEntry, haikuEntry},
	keyFlash:              {routerEntry, opusEntry, flashEntry},
	keyRouterOnly:         {routerEntry},
	keyRouterSol:          {routerEntry, solEntry},
	keyMidContext:         {midEntry},
	keyFable:              {fableEntry, sonnetEntry},
	keyProduction:         productionEntries,
	keyProductionFallback: bareEntries("claude-haiku-4-5", "claude-opus-5-5", "claude-sonnet-5-5", "gpt-5.4", "gpt-6-sol"),
	keyMixedNames:         bareEntries("claude-haiku-5-5", "claude-haiku-5.5", "claude-sonnet", "claude-sonnet-5-5"),
	keyMaxInput:           maxInputEntries,
	keyShort:              {sonnetEntry},
	keyBare:               {sonnetEntry},
	keyLong:               {sonnetEntry},
	keyNoInfer:            {sonnetEntry},
	keyQuoted:             {`{"id":"bad\"alias","object":"model","created":0,"owned_by":"x"}`},
}

// gatewayDouble serves the catalogue and protocol-specific inference replies, journalling requests.
type gatewayDouble struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*http.Request
}

func newGatewayDouble(t *testing.T) *gatewayDouble {
	t.Helper()
	g := &gatewayDouble{}
	g.Server = httptest.NewTLSServer(http.HandlerFunc(g.serve))
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
	if r.Method == http.MethodPost && key == keyNoInfer {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.Method == http.MethodPost {
		switch r.URL.Path {
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"id":"synthetic","choices":[]}`))
		case "/v1/messages":
			_, _ = w.Write([]byte(`{"id":"synthetic","type":"message","content":[]}`))
		case "/v1/responses":
			_, _ = w.Write([]byte(`{"id":"synthetic","object":"response","status":"completed","error":null,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi","annotations":[]}]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
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
	runner   *fakeRunner
	fakeHome string
}

func newSession(t *testing.T, g *gatewayDouble) *session {
	t.Helper()
	root := t.TempDir()
	fakeHome := filepath.Join(root, "fakehome")
	project := filepath.Join(root, "project")
	for _, d := range []string{fakeHome, project} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	environ := []string{"PATH=/usr/bin:/bin", config.HomeVar + "=" + fakeHome}
	if runtime.GOOS == "windows" {
		programFiles := filepath.Join(root, "ProgramFiles")
		writeFile(t, filepath.Join(programFiles, "Git", "bin", "bash.exe"), "")
		environ = append(environ, "ProgramFiles="+programFiles)
	}
	s := &session{
		t: t,
		env: config.Env{
			BaseURL: g.URL, UserHome: fakeHome, WorkDir: project,
			Environ: environ,
		},
		out:      &bytes.Buffer{},
		errb:     &bytes.Buffer{},
		runner:   &fakeRunner{},
		fakeHome: fakeHome,
	}
	clock := fixedNow
	tick := func() time.Time {
		now := clock
		clock = clock.Add(time.Second)
		return now
	}
	s.deps = cli.Deps{Stdin: strings.NewReader(""), Stdout: s.out, Stderr: s.errb, HTTP: g.Client(), Runner: s.runner, Now: tick}
	return s
}

func (s *session) stdin(text string) *session {
	s.deps.Stdin = strings.NewReader(text)
	return s
}

func (s *session) setupAndLaunch(tool string, native ...string) result {
	s.t.Helper()
	if r := s.run("setup", tool, "--yes"); r.code != 0 {
		s.t.Fatalf("setup %s failed: %s", tool, r.stderr)
	}
	if len(s.runner.calls) != 0 {
		s.t.Fatal("setup started a harness")
	}
	return s.run(append([]string{"launch", tool, "--"}, native...)...)
}

func (s *session) run(args ...string) result {
	s.t.Helper()
	s.out.Reset()
	s.errb.Reset()
	code := cli.Run(context.Background(), args, s.env, s.deps)
	return result{code, s.out.String(), s.errb.String()}
}

// keyPath is the key file of the session's gateway.
func (s *session) keyPath() string { return creds.Path(s.fakeHome, s.env.BaseURL) }

// latestBackup reads the newest backup a run made of path, or "" when there is none.
func latestBackup(t *testing.T, path string) string {
	t.Helper()
	baks, err := filepath.Glob(path + ".aiven-*.bak")
	if err != nil || len(baks) == 0 {
		return ""
	}
	data, err := os.ReadFile(slices.MaxFunc(baks, func(a, b string) int {
		sa, na := backupRank(a)
		sb, nb := backupRank(b)
		return cmp.Or(strings.Compare(sa, sb), cmp.Compare(na, nb))
	}))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// backupRank orders backups by their time, then by the -N a same-second rerun adds, which sorts before .bak as text.
func backupRank(name string) (string, int) {
	stem := strings.TrimSuffix(name, ".bak")
	if i := strings.LastIndex(stem, "Z-"); i >= 0 {
		if n, err := strconv.Atoi(stem[i+2:]); err == nil {
			return stem[:i+1], n
		}
	}
	return stem, 1
}

// loggedIn stores key as `login` would, without going through the launcher.
func (s *session) loggedIn(key string) *session {
	s.t.Helper()
	if _, err := creds.Write(s.fakeHome, s.env.BaseURL, key, fsutil.WriteAtomic); err != nil {
		s.t.Fatal(err)
	}
	return s
}

// configured writes what setup with --yes writes for tool, from keyValid's catalogue, without going through the
// launcher.
func (s *session) configured(tool string) *session {
	s.t.Helper()
	table, err := gateway.Client{HTTP: s.deps.HTTP, BaseURL: s.env.BaseURL}.Models(context.Background(), keyValid)
	if err != nil {
		s.t.Fatal(err)
	}
	in := harness.Input{Table: table, BaseURL: s.env.BaseURL, UserHome: s.fakeHome}
	var plan harness.Plan
	switch tool {
	case "claude":
		plan, err = claude.Setup(claude.Input{Table: table, BaseURL: s.env.BaseURL, UserHome: s.fakeHome, WorkDir: s.env.WorkDir})
	case "codex":
		plan, err = codex.Setup(in)
	case "pi":
		plan, err = pi.Setup(in)
	case "opencode":
		plan, err = opencode.Setup(in)
	case "omp":
		plan, err = omp.Setup(in)
	default:
		s.t.Fatalf("no setup for %s", tool)
	}
	if err != nil {
		s.t.Fatal(err)
	}
	for _, f := range plan.Files {
		if err := fsutil.WriteAtomic(f.Path, f.Data, 0o600); err != nil {
			s.t.Fatal(err)
		}
	}
	return s
}

// homeFiles lists every file under the user's home and the project except the key file.
func (s *session) homeFiles() []string {
	var files []string
	for _, root := range []string{s.fakeHome, s.env.WorkDir} {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && p != s.keyPath() {
				files = append(files, p)
			}
			return nil
		})
	}
	return files
}

// launched is the whole stderr of a launch that lists n models and starts product: no inference stage, no table.
func launched(origin string, n int, product string) string {
	return fmt.Sprintf("Gateway: %s\n  Retrieving available models…\n  OK %d models available\n  Starting %s; the session now owns this terminal.\n", origin, n, product)
}

// catalogueFailed is the whole stderr of a run whose model listing failed with category, then reported msg.
func catalogueFailed(category, msg string) string {
	return "  Retrieving available models…\n  FAILED " + category + "\naiven-ai-gateway: " + msg + "\n"
}
