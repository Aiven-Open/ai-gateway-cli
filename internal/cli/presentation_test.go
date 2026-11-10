// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/cli"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

// lockedBuffer is a stream the gateway double's handler reads while the command writes it.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// observed is one request with what stderr had shown and whether the key file existed when it arrived.
type observed struct {
	method, path, stderr string
	keySaved             bool
}

// observe routes the double through a recorder of what the user had been told before each request.
func observe(t *testing.T, s *session, g *gatewayDouble) (*lockedBuffer, func() []observed) {
	t.Helper()
	stderr := &lockedBuffer{}
	s.deps.Stderr = stderr
	var mu sync.Mutex
	var seen []observed
	g.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := os.Stat(s.keyPath())
		mu.Lock()
		seen = append(seen, observed{r.Method, r.URL.Path, stderr.String(), err == nil})
		mu.Unlock()
		g.serve(w, r)
	})
	return stderr, func() []observed {
		mu.Lock()
		defer mu.Unlock()
		return append([]observed(nil), seen...)
	}
}

func TestSetupAnnouncesEachRequestBeforeSendingItAndSavesOnlyAfterTheProbe(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).stdin(keyValid)
	stderr, seen := observe(t, s, g)
	s.deps.Prompter = &scriptedPrompter{answers: []string{"all"}}
	code := cli.Run(t.Context(), []string{"setup", "--key-stdin", "--yes"}, s.env, s.deps)
	reqs := seen()
	// The listing, then one check per distinct protocol and model: Claude Code checks Messages with glm and Pi with
	// claude-sonnet; Pi checks Chat Completions with aiven-model and OpenCode with claude-sonnet.
	if code != 0 || len(reqs) != 6 {
		t.Fatalf("exit %d, %d requests, stderr %q", code, len(reqs), stderr.String())
	}
	list := reqs[0]
	if list.method != http.MethodGet || list.path != "/v1/models" || !strings.HasSuffix(list.stderr, "Retrieving available models…\n") || list.keySaved {
		t.Errorf("listing arrived after %q (key saved %v)", list.stderr, list.keySaved)
	}
	for _, probe := range reqs[1:] {
		if probe.method != http.MethodPost || !strings.Contains(probe.stderr, "Basic check: ") ||
			!strings.HasSuffix(probe.stderr, "; it may incur usage…\n") || probe.keySaved {
			t.Errorf("probe arrived after %q (key saved %v)", probe.stderr, probe.keySaved)
		}
	}
	out := stderr.String()
	probe := reqs[len(reqs)-1]
	after := out[len(probe.stderr):]
	if !strings.HasPrefix(after, "  OK Responses with gpt-sol: HTTP 200; basic check passed\n  Saving the key…\n  OK saved to "+s.keyPath()+"\n") {
		t.Errorf("after the probe: %q", after)
	}
	// Each configured protocol and model is checked once before saving the key.
	for _, tool := range []string{"Pi", "OpenCode", "Codex", "Claude Code", "OMP"} {
		if !strings.Contains(out, "\n"+tool+" configuration\n") || !strings.Contains(out, "\n  "+tool+strings.Repeat(" ", 13-len(tool))+"configured\n") {
			t.Errorf("%s was not configured and summarised: %q", tool, out)
		}
	}
}

func TestLoginAndLaunchSendNoInferenceRequest(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).stdin(keyValid)
	stderr, seen := observe(t, s, g)
	if code := cli.Run(t.Context(), []string{"login"}, s.env, s.deps); code != 0 {
		t.Fatalf("login: exit %d, stderr %q", code, stderr.String())
	}
	if code := cli.Run(t.Context(), []string{"setup", "codex", "--yes"}, s.env, s.deps); code != 0 {
		t.Fatalf("setup: exit %d, stderr %q", code, stderr.String())
	}
	before := len(seen())
	if code := cli.Run(t.Context(), []string{"launch", "codex"}, s.env, s.deps); code != 0 {
		t.Fatalf("launch: exit %d, stderr %q", code, stderr.String())
	}
	reqs := seen()
	posts := 0
	for i, r := range reqs {
		if r.method == http.MethodPost {
			posts++
		}
		if i == 0 && (r.keySaved || !strings.HasSuffix(r.stderr, "Retrieving available models…\n")) {
			t.Errorf("login listing arrived after %q (key saved %v)", r.stderr, r.keySaved)
		}
	}
	// setup reuses the saved key and still probes once; login and launch never do.
	if posts != 1 || len(reqs)-before != 1 || reqs[len(reqs)-1].method != http.MethodGet {
		t.Errorf("requests %+v, want one probe, from setup", reqs)
	}
}

func TestSetupSummaryNamesEachOutcomeAndItsRecovery(t *testing.T) {
	codexProfile := filepath.FromSlash(".codex/aiven.config.toml")
	for _, tc := range []struct {
		name    string
		prepare func(*session)
		args    []string
		code    int
		want    []string
		absent  []string
	}{
		{
			name: "configured", args: []string{"--yes"},
			want:   []string{"Setup complete\n", "  Codex       configured\n", "Start a session\n  aiven-ai-gateway launch codex\n", "Or start the native tool directly\n  codex -p aiven\n", "Optional bash/zsh shortcuts\n", "  alias codex-aiven='aiven-ai-gateway launch codex --'\n"},
			absent: []string{"Needs attention", "Configure later", "alias aiven-codex="},
		},
		{
			name: "declined", prepare: func(s *session) { s.deps.Prompter = &answerPrompter{answer: "n"} },
			want:   []string{"Setup finished; some tools were not configured\n", "  Codex       declined; no files written\n", "Configure later\n  aiven-ai-gateway setup codex\n"},
			absent: []string{"Start a session", "alias ", "Setup complete"},
		},
		{
			name: "missing executable", args: []string{"--yes"}, code: 1,
			prepare: func(s *session) { s.runner.missing = []string{"codex"} },
			want:    []string{"Setup finished with errors\n", "  Codex       not installed", "Needs attention\n  aiven-ai-gateway setup codex\n"},
			absent:  []string{"Start a session", "alias "},
		},
		{
			name: "shared failure", args: []string{"--yes"}, code: 1,
			prepare: func(s *session) { s.stdin("sk-unknown") },
			want:    []string{"  FAILED the gateway rejected the key\n", "Setup finished with errors\n", "  Credential  not saved by this run; the model access check failed\n", "  Codex       not configured; not attempted: the model access check failed\n", "Needs attention\n  aiven-ai-gateway setup codex\n"},
			absent:  []string{"Start a session", "Saving the key"},
		},
		{
			name: "partial write", args: []string{"--yes"}, code: 1,
			prepare: func(s *session) {
				s.deps.WriteFile = func(path string, data []byte, perm os.FileMode) error {
					if strings.HasSuffix(path, codexProfile) {
						return errors.New("disk full")
					}
					return fsutil.WriteAtomic(path, data, perm)
				}
			},
			want:   []string{"Setup finished with errors\n", "  Codex       partially configured; ", "written: ", filepath.FromSlash(".codex/config.toml") + " (new file)\n", "Review the written files and recovery copies for Codex, then run:\n  aiven-ai-gateway setup codex\n"},
			absent: []string{"Start a session", "alias "},
		},
		{
			name: "cancelled at the default question", code: 1,
			prepare: func(s *session) { s.deps.Prompter = &scriptedPrompter{} },
			want:    []string{"Setup cancelled\n", "  Credential  not saved by this run; no key was read\n", "  Codex       not configured; not attempted: setup was cancelled before an answer\n"},
			absent:  []string{"Start a session", "Retrieving available models"},
		},
		{
			name: "end of input at the apply question", code: 1,
			prepare: func(s *session) { s.deps.Prompter = &scriptedPrompter{answers: []string{""}} },
			want:    []string{"Setup cancelled\n", "  Credential  saved to ", "  Codex       cancelled; no files written\n", "Needs attention\n  aiven-ai-gateway setup codex\n"},
			absent:  []string{"Start a session", "alias ", "written: "},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g).stdin(keyValid)
			if tc.prepare != nil {
				tc.prepare(s)
			}
			r := s.run(append([]string{"setup", "--key-stdin", "codex"}, tc.args...)...)
			if r.code != tc.code || r.stdout != "" {
				t.Fatalf("exit %d, want %d; stdout %q, stderr %q", r.code, tc.code, r.stdout, r.stderr)
			}
			for _, w := range tc.want {
				if !strings.Contains(r.stderr, w) {
					t.Errorf("stderr lacks %q:\n%s", w, r.stderr)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(r.stderr, a) {
					t.Errorf("stderr has %q:\n%s", a, r.stderr)
				}
			}
			if tc.code != 0 && tc.name != "partial write" && tc.name != "missing executable" {
				if _, err := os.Stat(filepath.Join(s.fakeHome, codexProfile)); err == nil {
					t.Error("profile written by a failed run")
				}
			}
		})
	}
}

func TestStylingFollowsEachStreamAndThePlainSwitches(t *testing.T) {
	width := func() int { return 200 }
	for _, tc := range []struct {
		name                string
		term                cli.Terminal
		environ, args       []string
		stdoutANSI, errANSI bool
		wantColumnsOnStdout bool
	}{
		{name: "both redirected"},
		{name: "stderr terminal, stdout piped", term: cli.Terminal{StderrTTY: true, StderrWidth: width}, errANSI: true},
		{name: "stdout terminal", term: cli.Terminal{StdoutTTY: true, StdoutWidth: width, StderrTTY: true, StderrWidth: width}, stdoutANSI: true, errANSI: true, wantColumnsOnStdout: true},
		{name: "NO_COLOR", term: cli.Terminal{StdoutTTY: true, StdoutWidth: width, StderrTTY: true, StderrWidth: width}, environ: []string{"NO_COLOR=1"}, wantColumnsOnStdout: true},
		{name: "TERM=dumb", term: cli.Terminal{StdoutTTY: true, StdoutWidth: width, StderrTTY: true, StderrWidth: width}, environ: []string{"TERM=dumb"}, wantColumnsOnStdout: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
			s.deps.Term = tc.term
			s.env.Environ = append(s.env.Environ, tc.environ...)
			r := s.run("login")
			if r.code != 0 {
				t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
			}
			if strings.Contains(r.stdout, "\x1b") != tc.stdoutANSI || strings.Contains(r.stderr, "\x1b") != tc.errANSI {
				t.Errorf("stdout %q, stderr %q; want escapes on stdout %v, stderr %v", r.stdout, r.stderr, tc.stdoutANSI, tc.errANSI)
			}
			// Several entries share a line only on a terminal wide enough.
			if columns := slices.ContainsFunc(strings.Split(r.stdout, "\n"), func(l string) bool { return strings.Contains(l, "claude-opus") && strings.Contains(l, "claude-sonnet") }); columns != tc.wantColumnsOnStdout {
				t.Errorf("columns on stdout = %v, want %v:\n%s", columns, tc.wantColumnsOnStdout, r.stdout)
			}
			if strings.Contains(r.stdout, "Retrieving") || strings.Contains(r.stderr, "Anthropic") {
				t.Errorf("streams mixed: stdout %q, stderr %q", r.stdout, r.stderr)
			}
		})
	}
}

func TestHelpGoesToStdoutAndNamesThePlainMode(t *testing.T) {
	r := newSession(t, newGatewayDouble(t)).run("--help")
	for _, want := range []string{"--plain", "NO_COLOR", "v shows the full\n      redacted diff", "Help and the model list go to stdout"} {
		if r.code != 0 || r.stderr != "" || !strings.Contains(r.stdout, want) {
			t.Errorf("help lacks %q: exit %d, stdout %q, stderr %q", want, r.code, r.stdout, r.stderr)
		}
	}
	if strings.Contains(r.stdout, "price") {
		t.Errorf("help still mentions prices: %q", r.stdout)
	}
}

func TestCancellationStopsTheRemainingTools(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	// The picker, one default question per tool, then end of input at the first apply question.
	p := &scriptedPrompter{answers: []string{"all", "", "", "", "", ""}}
	s.deps.Prompter = p
	r := s.run("setup", "--key-stdin")
	if r.code != 1 || !strings.Contains(r.stderr, "Setup cancelled\n") || strings.Count(r.stderr, "not attempted after the cancellation") != len(toolFiles)-1 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if applies := slices.DeleteFunc(p.prompts, func(q string) bool { return !strings.HasPrefix(q, "Apply this change") }); len(applies) != 1 {
		t.Errorf("apply questions after the cancellation: %q", applies)
	}
	for tool, file := range toolFiles {
		if _, err := os.Stat(filepath.Join(s.fakeHome, file)); err == nil {
			t.Errorf("%s configured after the cancellation", tool)
		}
	}
}
