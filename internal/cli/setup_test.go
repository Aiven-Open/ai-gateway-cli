// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"cmp"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
)

// answerPrompter answers every ReadLine and ReadSecret with fixed values and records the prompts.
type answerPrompter struct {
	answer, secret string
	prompts        []string
}

func (p *answerPrompter) ReadLine(prompt string) (string, error) {
	p.prompts = append(p.prompts, prompt)
	return p.answer, nil
}

func (p *answerPrompter) ReadSecret(prompt string) (string, error) {
	p.prompts = append(p.prompts, prompt)
	return p.secret, nil
}

func isPickPrompt(p string) bool {
	return strings.HasPrefix(p, "Numbers or names") || strings.HasPrefix(p, "Apply this change") || strings.HasPrefix(p, "Make Aiven")
}

type connectCase struct {
	name, cmd, stdin, fileKey, otherKey, wantKey, wantErr string
	args, environ                                         []string
	reused                                                bool
	prompter                                              *answerPrompter
	wantPrompts                                           []string
}

func TestSetupConfirmsEnvironmentKeyBeforeAnyRequest(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g)
	s.env.Environ = append(s.env.Environ, creds.EnvVar+"="+keyLong)
	asked := false
	s.deps.Prompter = hookPrompter{
		on: "Use this key with " + g.URL + "? [y/N] ",
		hook: func() {
			asked = true
			if len(g.journal()) != 0 {
				t.Errorf("gateway received a request before credential confirmation")
			}
			for _, want := range []string{"Gateway: " + g.URL, "Key source: " + creds.EnvVar, "Key: AVNS_…wxyz"} {
				if !strings.Contains(s.errb.String(), want) {
					t.Errorf("confirmation did not show %q: %s", want, s.errb.String())
				}
			}
		},
	}
	r := s.run("setup", "omp", "--yes")
	if !asked {
		t.Errorf("setup did not ask to send the environment key to %s", g.URL)
	}
	if r.code != 0 || r.stdout != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if reqs := g.journal(); len(reqs) == 0 || reqs[0].Header.Get("Authorization") != "Bearer "+keyLong {
		t.Errorf("approved key was not used for the first request: %v", reqs)
	}
}

func TestSetupResolvesChecksAndSavesKey(t *testing.T) {
	hidden := "Paste your Aiven AI Gateway API key (hidden): "
	keep := "Keep the saved key (sk-…)? [Y/n] "
	envConsent := "Use this key with <gateway>? [y/N] "
	cases := []connectCase{
		{name: "stdin flag", args: []string{"--key-stdin"}, stdin: keyValid + "\n", prompter: &answerPrompter{}, wantKey: keyValid},
		{name: "explicit setup", cmd: "setup", args: []string{"--key-stdin"}, stdin: keyValid, wantKey: keyValid},
		{name: "key file kept on enter", fileKey: keyValid, prompter: &answerPrompter{}, wantPrompts: []string{keep}, wantKey: keyValid},
		{name: "key file kept with yes", args: []string{"--yes"}, fileKey: keyValid, prompter: &answerPrompter{answer: "n"}, wantKey: keyValid, reused: true},
		{name: "key file kept with -y", args: []string{"-y"}, fileKey: keyValid, prompter: &answerPrompter{answer: "n"}, wantKey: keyValid, reused: true},
		{name: "key file kept without terminal", fileKey: keyValid, stdin: keyClaudeOnly, wantKey: keyValid, reused: true},
		{name: "key file replaced", fileKey: keyValid, prompter: &answerPrompter{answer: " N ", secret: keyClaudeOnly}, wantPrompts: []string{keep, hidden}, wantKey: keyClaudeOnly},
		{name: "stdin flag overrides key file", args: []string{"--key-stdin"}, fileKey: keyValid, stdin: keyClaudeOnly, prompter: &answerPrompter{}, wantKey: keyClaudeOnly},
		{name: "short key shows prefix only", fileKey: keyShort, prompter: &answerPrompter{}, wantPrompts: []string{"Keep the saved key (sk-…)? [Y/n] "}, wantKey: keyShort},
		{name: "bare 12-char key fully hidden", fileKey: keyBare, prompter: &answerPrompter{}, wantPrompts: []string{"Keep the saved key (…)? [Y/n] "}, wantKey: keyBare},
		{name: "long key shows last 4", fileKey: keyLong, prompter: &answerPrompter{}, wantPrompts: []string{"Keep the saved key (AVNS_…wxyz)? [Y/n] "}, wantKey: keyLong},
		{name: "environment key saved", environ: []string{"AIVEN_AI_GATEWAY_KEY=" + keyFlash}, prompter: &answerPrompter{answer: "y"}, wantPrompts: []string{envConsent}, wantKey: keyFlash},
		{name: "rejected key file falls back to environment", fileKey: "sk-expired", environ: []string{"AIVEN_AI_GATEWAY_KEY=" + keyFlash}, prompter: &answerPrompter{answer: "y"}, wantPrompts: []string{envConsent}, wantKey: keyFlash},
		{name: "no-model saved key falls back to environment", fileKey: keyEmpty, environ: []string{"AIVEN_AI_GATEWAY_KEY=" + keyFlash}, prompter: &answerPrompter{answer: "y"}, wantPrompts: []string{envConsent}, wantKey: keyFlash},
		{name: "no-model saved key prompts", fileKey: keyEmpty, prompter: &answerPrompter{secret: keyValid}, wantPrompts: []string{hidden}, wantKey: keyValid},
		{name: "no-model environment key prompts", environ: []string{"AIVEN_AI_GATEWAY_KEY=" + keyEmpty}, prompter: &answerPrompter{answer: "y", secret: keyValid}, wantPrompts: []string{envConsent, hidden}, wantKey: keyValid},
		{name: "request refusal does not switch to environment", fileKey: keyNoInfer, environ: []string{"AIVEN_AI_GATEWAY_KEY=" + keyValid}, prompter: &answerPrompter{}, wantErr: "Messages basic check failed: gateway returned HTTP 403"},
		{name: "request refusal does not prompt for replacement", fileKey: keyNoInfer, prompter: &answerPrompter{secret: keyValid}, wantErr: "Messages basic check failed: gateway returned HTTP 403"},
		{name: "new key request refusal", args: []string{"--key-stdin"}, stdin: keyNoInfer, wantErr: "Messages basic check failed: gateway returned HTTP 403"},
		{name: "unrecognised keep answer", fileKey: keyValid, prompter: &answerPrompter{answer: "maybe"}, wantErr: "expected y or n"},
		{name: "expired key file prompts", fileKey: "sk-expired", environ: []string{"AIVEN_AI_GATEWAY_KEY=sk-expired"}, prompter: &answerPrompter{secret: keyValid}, wantPrompts: []string{hidden}, wantKey: keyValid},
		{name: "key file for another gateway prompts", otherKey: keyFlash, prompter: &answerPrompter{secret: keyValid}, wantPrompts: []string{hidden}, wantKey: keyValid},
		{name: "stale environment key prompts", environ: []string{"AIVEN_AI_GATEWAY_KEY=sk-expired"}, prompter: &answerPrompter{answer: "y", secret: keyValid}, wantPrompts: []string{envConsent, hidden}, wantKey: keyValid},
		{name: "no implicit stdin without terminal", stdin: keyValid, wantErr: "no usable saved key"},
		{name: "clipboard flag is unknown", args: []string{"--key-from-clipboard"}, wantErr: "unexpected argument"},
		{name: "rejected key", args: []string{"--key-stdin"}, stdin: "sk-unknown", wantErr: "key rejected by gateway (401)"},
		{name: "empty model table", args: []string{"--key-stdin"}, stdin: keyEmpty, wantErr: "key grants no models"},
		{name: "unexpected arg", args: []string{keyValid}, wantErr: "unexpected argument"},
		{name: "global flag is unknown", args: []string{"--key-stdin", "--global", "claude"}, stdin: keyValid, wantErr: "unexpected argument"},
	}
	for _, flag := range []string{"--key", "--api-key", "--key=" + keyValid, "--api-key=" + keyValid} {
		cases = append(cases, connectCase{name: "argv " + flag, args: []string{flag}, wantErr: "do not pass API keys on the command line"})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g).stdin(tc.stdin)
			if tc.fileKey != "" {
				s.loggedIn(tc.fileKey)
			}
			if tc.otherKey != "" {
				if _, err := creds.Write(s.fakeHome, config.DefaultBaseURL, tc.otherKey, fsutil.WriteAtomic); err != nil {
					t.Fatal(err)
				}
			}
			if tc.prompter != nil {
				s.deps.Prompter = tc.prompter
			}
			s.env.Environ = append(s.env.Environ, tc.environ...)
			r := s.run(append([]string{cmp.Or(tc.cmd, "setup"), "omp"}, tc.args...)...)
			keyFile := s.keyPath()
			if tc.wantErr != "" {
				if r.code != 1 || !strings.Contains(r.stderr, tc.wantErr) || strings.Contains(r.stderr, keyValid) {
					t.Fatalf("exit %d, stderr %q, want %q without key", r.code, r.stderr, tc.wantErr)
				}
				if data, err := os.ReadFile(keyFile); tc.fileKey == "" && !os.IsNotExist(err) {
					t.Errorf("key file written on failure: %v", err)
				} else if tc.fileKey != "" && string(data) != tc.fileKey+"\n" {
					t.Errorf("key file = %q, %v; want %q unchanged", data, err, tc.fileKey)
				}
				return
			}
			if r.code != 0 || r.stdout != "" || !strings.Contains(r.stderr, "OK saved to "+keyFile+"\n") {
				t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
			if reqs := g.journal(); reqs[len(reqs)-1].Method != "POST" {
				t.Errorf("last request %s %s, want completion probe", reqs[len(reqs)-1].Method, reqs[len(reqs)-1].URL)
			}
			// The tool picker follows every bare connect on a terminal; this test is about the key prompts before it.
			wantPrompts := strings.ReplaceAll(strings.Join(tc.wantPrompts, "|"), "<gateway>", g.URL)
			if tc.prompter != nil && strings.Join(slices.DeleteFunc(tc.prompter.prompts, isPickPrompt), "|") != wantPrompts {
				t.Errorf("prompts = %q, want %q", tc.prompter.prompts, wantPrompts)
			}
			if data, err := os.ReadFile(keyFile); err != nil || string(data) != tc.wantKey+"\n" {
				t.Errorf("key file = %q, %v; want %q", data, err, tc.wantKey)
			}
			if tc.otherKey != "" && readOrEmpty(t, creds.Path(s.fakeHome, config.DefaultBaseURL)) != tc.otherKey+"\n" {
				t.Errorf("the other gateway's key file changed")
			}
			if tc.reused != strings.Contains(r.stderr, "Using your existing gateway key.") {
				t.Errorf("stderr %q, want reused %v", r.stderr, tc.reused)
			}
			if tc.fileKey != "" && slices.Contains(tc.environ, "AIVEN_AI_GATEWAY_KEY="+tc.fileKey) {
				count := 0
				for _, req := range g.journal() {
					if req.Method == http.MethodGet && req.Header.Get("Authorization") == "Bearer "+tc.fileKey {
						count++
					}
				}
				if count != 1 {
					t.Errorf("same rejected key checked %d times, want one", count)
				}
			}
		})
	}
}

func TestSetupKeepPromptEndOfInputSavesNothing(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	keyFile := s.keyPath()
	before, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	p := &scriptedPrompter{}
	s.deps.Prompter = p
	if r := s.run("setup", "omp"); r.code != 1 || !strings.Contains(r.stderr, "no answer: input ended") || len(p.prompts) != 1 {
		t.Fatalf("exit %d, stderr %q, prompts %q", r.code, r.stderr, p.prompts)
	}
	if after, err := os.ReadFile(keyFile); err != nil || string(after) != string(before) {
		t.Errorf("key file = %q, %v; want %q", after, err, before)
	}
}

func TestSetupPromptsWithoutEcho(t *testing.T) {
	s := newSession(t, newGatewayDouble(t))
	p := &answerPrompter{secret: keyValid}
	s.deps.Prompter = p
	r := s.run("setup", "omp")
	if r.code != 0 || !slices.Contains(p.prompts, "Paste your Aiven AI Gateway API key (hidden): ") {
		t.Fatalf("exit %d, prompts %q, stderr %q", r.code, p.prompts, r.stderr)
	}
	if strings.Contains(r.stdout+r.stderr, keyValid) {
		t.Errorf("key echoed:\n%s%s", r.stdout, r.stderr)
	}
}

func TestSetupNeedsAHome(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	s.env.UserHome = ""
	if r := s.run("setup", "omp", "--key-stdin"); r.code != 1 || !strings.Contains(r.stderr, "Set "+config.HomeVar) {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestSetupSurfacesAFailedTestRequest(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"object":"list","data":[` + sonnetEntry + `]}`))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	s := newSession(t, &gatewayDouble{Server: srv}).stdin(keyValid)
	if r := s.run("setup", "omp", "--key-stdin"); r.code != 1 || !strings.Contains(r.stderr, "Messages basic check failed: gateway returned HTTP 502") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
		t.Errorf("key file written: %v", err)
	}
}

func TestSetupSurfacesGatewayOutage(t *testing.T) {
	srv502 := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer srv502.Close()
	s := newSession(t, newGatewayDouble(t))
	s.env.BaseURL = srv502.URL
	s.deps.HTTP = srv502.Client()
	s.loggedIn(keyValid)
	s.deps.Prompter = &answerPrompter{}
	if r := s.run("setup", "omp"); r.code != 1 || !strings.Contains(r.stderr, "HTTP 502") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestSetupConfiguresNamedTool(t *testing.T) {
	for _, tc := range []struct {
		tool  string
		args  []string
		files []string
		run   string
	}{
		{"pi", []string{"--key-stdin", "--yes", "pi"}, []string{".pi/agent/models.json"}, "pi --model aiven/claude-sonnet\n"},
		{"opencode", []string{"opencode", "--key-stdin", "-y"}, []string{".config/opencode/opencode.json"}, "opencode -m aiven/claude-sonnet\n"},
		{"codex", []string{"codex", "--key-stdin", "--yes"}, []string{".codex/config.toml", ".codex/aiven.config.toml"}, "codex -p aiven\n"},
		{"omp", []string{"omp", "--key-stdin", "--yes"}, []string{".omp/agent/models.yml"}, "omp --model aiven/claude-sonnet\n"},
	} {
		s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
		r := s.run(append([]string{"setup"}, tc.args...)...)
		if r.code != 0 || len(s.runner.calls) != 0 || !strings.Contains(r.stderr, "native default left as it was\n") || !strings.Contains(r.stderr, "Or start the native tool directly\n  "+tc.run) || !strings.Contains(r.stderr, "Start a session\n  aiven-ai-gateway launch "+tc.tool+"\n") {
			t.Fatalf("exit %d, stderr %q, calls %v", r.code, r.stderr, s.runner.calls)
		}
		for _, f := range tc.files {
			path := filepath.Join(s.fakeHome, f)
			if data, err := os.ReadFile(path); err != nil || !strings.Contains(r.stderr, "written: "+path+" (new file)\n") || !strings.Contains(string(data), "aiven") || strings.Contains(string(data), keyValid) {
				t.Errorf("%s = %q (%v), stderr %q", f, data, err, r.stderr)
			}
		}
	}
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	writeFile(t, filepath.Join(s.fakeHome, ".pi/agent/models.json"), "{bad")
	if r := s.run("setup", "--key-stdin", "pi"); r.code != 1 || !strings.Contains(r.stderr, "not a JSON object") {
		t.Fatalf("bad config: exit %d, stderr %q", r.code, r.stderr)
	}
	if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
		t.Errorf("key saved despite the bad config: %v", err)
	}
}

func TestSetupNamesTheBackupOfAReplacedConfig(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	path := filepath.Join(s.fakeHome, ".pi/agent/models.json")
	writeFile(t, path, `{"providers":{}}`)
	first := fsutil.BackupPath(path, fixedNow)
	note := " (previous version: " + first + ")"
	r := s.run("setup", "--key-stdin", "--yes", "pi")
	if r.code != 0 || !strings.Contains(r.stderr, "+++ "+path+note+"\n") || !strings.Contains(r.stderr, "written: "+path+note+"\n") {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if bak := latestBackup(t, path); bak != `{"providers":{}}` {
		t.Errorf("backup = %q", bak)
	}
	writeFile(t, path, `{"providers":{"other":{}}}`)
	// The second run reads the same second as the first, as a quick rerun would.
	s.deps.Now = func() time.Time { return fixedNow }
	if r := s.stdin(keyValid).run("setup", "--key-stdin", "--yes", "pi"); r.code != 0 || !strings.Contains(r.stderr, ".aiven-20261028T120000Z-2.bak)") {
		t.Fatalf("second run: exit %d, stderr %q", r.code, r.stderr)
	}
	kept, _ := os.ReadFile(first)
	if baks, _ := filepath.Glob(path + ".aiven-*.bak"); len(baks) != 2 || string(kept) != `{"providers":{}}` || latestBackup(t, path) != `{"providers":{"other":{}}}` {
		t.Errorf("backups after a second run = %v, first = %q", baks, kept)
	}
}

func TestSetupLeavesConfigUntouchedWhenItsBackupFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory write-permission bit")
	}
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	path := filepath.Join(s.fakeHome, ".pi/agent/models.json")
	const before = `{"providers":{"other":{}}}`
	writeFile(t, path, before)
	// A directory the backup cannot be written into; the config itself stays readable.
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if r := s.run("setup", "--key-stdin", "--yes", "pi"); r.code != 1 || !strings.Contains(r.stderr, "models.json.aiven-20261028T120000Z.bak") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if got, _ := os.ReadFile(path); string(got) != before {
		t.Errorf("models.json = %q, want it untouched", got)
	}
}

// hookPrompter answers y and runs hook before the prompt that starts with on.
type hookPrompter struct {
	on   string
	hook func()
}

func (p hookPrompter) ReadLine(prompt string) (string, error) {
	if strings.HasPrefix(prompt, p.on) {
		p.hook()
	}
	return "y", nil
}

func (p hookPrompter) ReadSecret(string) (string, error) { return "", io.EOF }

func TestSetupKeepsThePreviewedBackupNameWhenAnotherRunTakesIt(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	path := filepath.Join(s.fakeHome, ".codex/config.toml")
	const before = "model = 'o3'\n"
	writeFile(t, path, before)
	taken := fsutil.BackupPath(path, fixedNow)
	s.deps.Prompter = hookPrompter{"Apply this change", func() { writeFile(t, taken, "another run") }}
	r := s.run("setup", "--key-stdin", "codex")
	if r.code != 1 || !strings.Contains(r.stderr, "(previous version: "+taken+")") || !strings.Contains(r.stderr, taken+" already exists; retry") {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if got, _ := os.ReadFile(path); string(got) != before {
		t.Errorf("config.toml = %q, want it untouched", got)
	}
}

func TestSetupPreviewsAToolChangeBeforeWritingIt(t *testing.T) {
	ask, dflt := "Apply this change to codex? [Y/n/v] ", "Make Aiven the native default for Codex? Enter keeps your current default. [y/N] "
	for _, tc := range []struct {
		name, want  string
		args        []string
		prompter    *answerPrompter
		wantPrompts []string
		written     bool
	}{
		{name: "applied on enter", prompter: &answerPrompter{}, wantPrompts: []string{dflt, ask}, written: true, want: "Codex       configured\n"},
		{name: "declined", prompter: &answerPrompter{answer: "n"}, wantPrompts: []string{dflt, ask}, want: "Codex       declined; no files written\n"},
		{name: "no terminal", want: "Codex       not configured; without a terminal, rerun with --yes to apply\n"},
		{name: "yes", args: []string{"--yes"}, prompter: &answerPrompter{answer: "n"}, written: true, want: "Codex       configured\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
			if tc.prompter != nil {
				s.deps.Prompter = tc.prompter
			}
			profile := filepath.Join(s.fakeHome, ".codex/aiven.config.toml")
			r := s.run(append([]string{"setup", "--key-stdin", "codex"}, tc.args...)...)
			if r.code != 0 || !strings.Contains(r.stderr, "+++ "+profile+" (new file)\n") || !strings.Contains(r.stderr, "+model_provider = 'aiven'") || !strings.Contains(r.stderr, tc.want) {
				t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
			if tc.prompter != nil && strings.Join(tc.prompter.prompts, "|") != strings.Join(tc.wantPrompts, "|") {
				t.Errorf("prompts = %q, want %q", tc.prompter.prompts, tc.wantPrompts)
			}
			if _, err := os.Stat(profile); (err == nil) != tc.written {
				t.Errorf("profile written = %v, want %v", err == nil, tc.written)
			}
			if _, err := os.Stat(s.keyPath()); err != nil {
				t.Errorf("key not saved: %v", err)
			}
		})
	}
}

// Syntax a tool's own reader rejects is removed, and the preview shows it go.
func TestSetupPreviewShowsSyntaxTheToolCannotReadRemoved(t *testing.T) {
	for _, tc := range []struct{ tool, file, src, removed string }{
		{"claude", ".claude/aiven.settings.json", "{\n  // mine\n  \"theme\": \"dark\",\n}\n", "\n-  // mine\n"},
		{"pi", ".pi/agent/models.json", "{\n  /* mine */\n  // kept\n  \"providers\": {}\n}\n", "\n-  /* mine */\n"},
	} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
		path := filepath.Join(s.fakeHome, tc.file)
		writeFile(t, path, tc.src)
		r := s.run("setup", tc.tool, "--yes")
		data, _ := os.ReadFile(path)
		if r.code != 0 || !strings.Contains(r.stderr, tc.removed) || strings.Contains(string(data), "mine") {
			t.Errorf("%s: exit %d, stderr %q, file %s", tc.tool, r.code, r.stderr, data)
		}
		if tc.tool == "claude" && !json.Valid(data) || tc.tool == "pi" && !strings.Contains(string(data), "// kept") {
			t.Errorf("%s: %s", tc.tool, data)
		}
	}
}

func TestSetupSummarisesModelListsAndSkipsAConfiguredTool(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).stdin(keyProduction)
	path := filepath.Join(s.fakeHome, ".pi/agent/models.json")
	r := s.run("setup", "--key-stdin", "--yes", "pi")
	// The provider's other fields are shown; only its model list is summarised.
	if r.code != 0 || !strings.Contains(r.stderr, "+++ "+path+" (new file)\n") || !strings.Contains(r.stderr, `+      "baseUrl": "`+g.URL+`/v1",`) ||
		!strings.Contains(r.stderr, `+      "models": [11 models: `) || !strings.Contains(r.stderr, ", … +9 more]\n") || strings.Contains(r.stderr, `"id":`) {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	p := &answerPrompter{}
	s.deps.Prompter = p
	r = s.stdin(keyProduction).run("setup", "--key-stdin", "pi")
	if r.code != 0 || !strings.Contains(r.stderr, "Already configured; no file needs a change.\n") || !strings.Contains(r.stderr, "Pi          unchanged\n") || !strings.Contains(r.stderr, "\n  pi --model aiven/claude-sonnet-5-5\n") || len(p.prompts) != 1 {
		t.Fatalf("exit %d, stderr %q, prompts %q", r.code, r.stderr, p.prompts)
	}
}

func TestSetupPreviewsRedactKeysAlreadyInAConfig(t *testing.T) {
	secrets := []string{
		"plainSecretValue123456", "AVNS_test.synthetic_secret", "multiLineSecret123", "commentSecret123", "urlSecret123", "cookieSecret123",
		// Values a looser rule took for commands, paths or variable names.
		"fixture pass phrase", "!bangSecret123", "/pathLikeSecret123", "UPPER_SECRET_123",
		// OpenCode reference syntax, which is a literal value in any other tool's file.
		"{env:SYNTHETIC_PASSWORD}", "{file:/synthetic-secret}",
		// Codex env_key values: a variable name under model_providers, a literal in an MCP server's env table, which the
		// preview does not tell apart.
		"SYNTHETIC_PROVIDER_KEY", "SYNTHETIC_PRIVATE_PASSWORD", "SYNTHETIC_DOTTED_PASSWORD",
	}
	// References the file's own tool reads as one say where a key is, so the preview keeps them.
	visible := map[string]string{"opencode": `"apiKey":"{env:OTHER_API_KEY}"`}
	files := map[string]map[string]string{
		"pi": {".pi/agent/models.json": `{"providers":{"other":{"apiKey":"` + secrets[0] + `"},"spaced":{"apiKey":"` + secrets[6] +
			`"},"bang":{"apiKey":"` + secrets[7] + `"},"ref":{"apiKey":"` + secrets[10] + `"}}}`},
		"opencode": {".config/opencode/opencode.json": "{\n  // token: " + secrets[3] + "\n" +
			`"provider":{"other":{"options":{"apiKey":"` + secrets[0] + `","baseURL":"https://user:` + secrets[4] + `@example.invalid/v1"}},` +
			`"ref":{"options":{"apiKey":"{env:OTHER_API_KEY}"}}}}`},
		"codex": {".codex/config.toml": "mcp_servers.dotted.env.env_key = \"" + secrets[14] + "\"\n" +
			"[model_providers.other]\nname = 'other'\nexperimental_bearer_token = '" + secrets[0] +
			"'\nhttp_headers = { Cookie = \"session=" + secrets[5] + "\" }\n\n[model_providers.multi]\nexperimental_bearer_token = \"\"\"" + secrets[2] + "\"\"\"\n" +
			"[model_providers.ref]\nname = 'ref'\nenv_key = \"" + secrets[12] + "\"\n\n" +
			"[mcp_servers.db.env]\nDATABASE_PASSWORD = \"" + secrets[6] + "\"\nAPI_TOKEN = \"" + secrets[8] + "\"\n" +
			"DB_PASSWORD = \"" + secrets[10] + "\"\nSECRET_FILE_PASSWORD = \"" + secrets[11] + "\"\n" +
			"env_key = \"" + secrets[13] + "\"\n"},
		// Setup edits config.toml in place, so its diff shows only the three lines before the appended Aiven table:
		// each further case ends with values the first case keeps out of that context.
		"codex providers": {".codex/config.toml": "[model_providers]\nother = { name = 'other', experimental_bearer_token = '" + secrets[0] +
			"', http_headers = { Cookie = \"session=" + secrets[5] + "\" } }\nmulti = { experimental_bearer_token = \"\"\"" + secrets[2] + "\"\"\" }\n" +
			"ref = { name = 'ref', env_key = \"" + secrets[12] + "\" }\n"},
		"codex mcp": {".codex/config.toml": "[mcp_servers.db.env]\nDATABASE_PASSWORD = \"" + secrets[6] + "\"\nAPI_TOKEN = \"" + secrets[8] + "\"\n"},
		"claude": {
			".claude/aiven.settings.json": `{"env":{"ANTHROPIC_AUTH_TOKEN":"` + secrets[1] + `","DATABASE_PASSWORD":"` + secrets[6] +
				`","SESSION_SECRET":"` + secrets[9] + `","DB_PASSWORD":"` + secrets[10] + `","FILE_PASSWORD":"` + secrets[11] + `"}}`,
			".claude/settings.json": `{"env":{"ANTHROPIC_API_KEY":"` + secrets[0] + `"}}`,
		},
	}
	for name, fs := range files {
		tool, _, _ := strings.Cut(name, " ")
		for _, mode := range []string{"--yes", "no terminal", "y", "n"} {
			t.Run(name+" "+mode, func(t *testing.T) {
				s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
				for rel, data := range fs {
					writeFile(t, filepath.Join(s.fakeHome, rel), data)
				}
				args := []string{"setup", "--key-stdin", tool}
				switch mode {
				case "--yes":
					args = append(args, "--yes")
				case "y", "n":
					s.deps.Prompter = &answerPrompter{answer: mode}
				}
				r := s.run(args...)
				out := r.stdout + r.stderr
				leaked := slices.ContainsFunc(secrets, func(v string) bool { return strings.Contains(out, v) })
				// The diff shows only the lines near a change, numbered across the whole file.
				if r.code != 0 || leaked || !strings.Contains(r.stderr, "<redacted ") || !strings.Contains(r.stderr, visible[tool]) {
					t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
				}
			})
		}
	}
}

func TestSetupMakesAivenTheDefaultOnlyWhenAsked(t *testing.T) {
	for _, tc := range []struct {
		tool, file, before, want string
	}{
		{"pi", ".pi/agent/settings.json", `{"defaultProvider":"anthropic","theme":"dark"}`, `"defaultModel": "claude-sonnet"`},
		{"opencode", ".config/opencode/opencode.json", `{"model":"anthropic/claude"}`, `"model": "aiven/claude-sonnet"`},
		{"codex", ".codex/config.toml", "model = 'o3'\n", "model_provider = 'aiven'"},
		{"claude", ".claude/settings.json", `{"theme":"dark"}`, `"apiKeyHelper": "`},
		{"omp", ".omp/agent/config.yml", "theme: dark\nmodelRoles:\n  default: other/model\n", "default: aiven/claude-sonnet"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t))
			path := filepath.Join(s.fakeHome, tc.file)
			writeFile(t, path, tc.before)
			ask := "Make Aiven the native default for " + productNames[tc.tool] + "? Enter keeps your current default. [y/N] "
			p := &answerPrompter{}
			s.deps.Prompter = p
			if r := s.stdin(keyValid).run("setup", "--key-stdin", tc.tool); r.code != 0 || !slices.Contains(p.prompts, ask) || !strings.Contains(r.stderr, "native default left as it was\n") {
				t.Fatalf("enter: exit %d, prompts %q, stderr %q", r.code, p.prompts, r.stderr)
			}
			if got, _ := os.ReadFile(path); strings.Contains(string(got), tc.want) {
				t.Fatalf("enter made Aiven the default: %s", got)
			}
			original, _ := os.ReadFile(path)
			if r := s.stdin(keyValid).run("setup", "--key-stdin", "--yes", tc.tool); r.code != 0 || !strings.Contains(r.stderr, "native default left as it was\n") {
				t.Fatalf("--yes: exit %d, stderr %q", r.code, r.stderr)
			}
			p.answer = "y"
			r := s.stdin(keyValid).run("setup", "--key-stdin", tc.tool)
			if r.code != 0 || !strings.Contains(r.stderr, "+++ "+path+" (previous version: "+path+".aiven-") || !strings.Contains(r.stderr, "Aiven is now the native default\n") || !strings.Contains(r.stderr, "directly\n  "+tc.tool+"\n") {
				t.Fatalf("y: exit %d, stderr %q", r.code, r.stderr)
			}
			if got, _ := os.ReadFile(path); !strings.Contains(string(got), tc.want) || strings.Contains(string(got), keyValid) {
				t.Errorf("%s = %s, want %s", tc.file, got, tc.want)
			}
			if tc.tool == "claude" && readJSON(t, path)["apiKeyHelper"] != claude.APIKeyHelper(s.env.BaseURL) {
				t.Errorf("apiKeyHelper = %q, want %q", readJSON(t, path)["apiKeyHelper"], claude.APIKeyHelper(s.env.BaseURL))
			}
			// One write per file per run, so the backup is the version from before the run, not an intermediate one.
			if bak := latestBackup(t, path); bak != string(original) {
				t.Errorf("backup = %q, want %q", bak, original)
			}
			fresh := newSession(t, newGatewayDouble(t)).stdin(keyValid)
			fresh.deps.Prompter = &answerPrompter{answer: "y"}
			writeFile(t, filepath.Join(fresh.fakeHome, tc.file), tc.before)
			if r := fresh.run("setup", "--key-stdin", tc.tool); r.code != 0 {
				t.Fatalf("fresh: exit %d, stderr %q", r.code, r.stderr)
			}
			if bak := latestBackup(t, filepath.Join(fresh.fakeHome, tc.file)); bak != tc.before {
				t.Errorf("fresh backup = %q, want %q", bak, tc.before)
			}
			p.prompts = nil
			if r := s.stdin(keyValid).run("setup", "--key-stdin", tc.tool); r.code != 0 || len(p.prompts) != 1 || !strings.Contains(r.stderr, "Already configured; no file needs a change.") ||
				strings.Contains(r.stderr, "Aiven is now the native default") || strings.Contains(r.stderr, "directly\n  "+tc.tool+"\n") {
				t.Fatalf("again: exit %d, prompts %q, stderr %q", r.code, p.prompts, r.stderr)
			}
		})
	}
}

func TestSetupChecksTheDefaultFileBeforeAsking(t *testing.T) {
	for _, tc := range []struct{ tool, provider, defaults string }{
		{"pi", ".pi/agent/models.json", ".pi/agent/settings.json"},
		{"claude", claude.SettingsFile, ".claude/settings.json"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
			p := &answerPrompter{answer: "y"}
			s.deps.Prompter = p
			writeFile(t, filepath.Join(s.fakeHome, tc.defaults), "{not json")
			r := s.run("setup", "--key-stdin", tc.tool)
			if r.code == 0 || !strings.Contains(r.stderr, "fix or move it, then retry") || slices.ContainsFunc(p.prompts, func(q string) bool { return strings.HasPrefix(q, "Apply") }) {
				t.Fatalf("exit %d, prompts %q, stdout %q, stderr %q", r.code, p.prompts, r.stdout, r.stderr)
			}
			if _, err := os.Stat(filepath.Join(s.fakeHome, tc.provider)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("%s written after the default file failed: %v", tc.provider, err)
			}
		})
	}
}

func TestSetupRefusesAToolWhoseConfigIsMoved(t *testing.T) {
	for _, tc := range []struct{ tool, name string }{
		{"pi", "PI_CODING_AGENT_DIR"},
		{"opencode", "OPENCODE_CONFIG"},
		{"opencode", "OPENCODE_CONFIG_DIR"},
		{"codex", "CODEX_HOME"},
		{"claude", "CLAUDE_CONFIG_DIR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
			s.env.Environ = append(s.env.Environ, tc.name+"="+filepath.Join(s.fakeHome, "elsewhere"))
			r := s.run("setup", "--key-stdin", "--yes", tc.tool)
			if r.code != 1 || !strings.Contains(r.stderr, tc.name+" is set, so "+tc.tool+" would not read the config aiven-ai-gateway writes") {
				t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
			// Refused before the key is read, so nothing is written at all.
			if entries, _ := os.ReadDir(s.fakeHome); len(entries) != 0 {
				t.Errorf("wrote %v", entries)
			}
			s.env.Environ[len(s.env.Environ)-1] = tc.name + "="
			if r := s.stdin(keyValid).run("setup", "--key-stdin", "--yes", tc.tool); r.code != 0 {
				t.Errorf("empty %s: exit %d, stderr %q", tc.name, r.code, r.stderr)
			}
		})
	}
}

func TestTopLevelKeyFlagIsRefusedWithoutEchoingIt(t *testing.T) {
	for _, args := range [][]string{{"--key=" + keyValid}, {"--api-key=" + keyValid}, {"--key", keyValid}, {"--api-key", keyValid}} {
		t.Run(args[0], func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g)
			r := s.run(args...)
			if r.code != 1 || !strings.Contains(r.stderr, "do not pass API keys on the command line; use login or setup --key-stdin") || strings.Contains(r.stderr, keyValid) {
				t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
			}
			if len(g.journal()) != 0 || len(s.homeFiles()) != 0 {
				t.Error("a refused key flag had side effects")
			}
		})
	}
}

func TestUnrecognisedAnswerIsAskedAgain(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	ask := "Make Aiven the native default for Pi? Enter keeps your current default. [y/N] "
	p := &scriptedPrompter{answers: []string{"maybe", "maybe", "y", "y"}}
	s.deps.Prompter = p
	r := s.run("setup", "--key-stdin", "pi")
	if r.code != 0 || len(p.prompts) < 3 || !slices.Equal(p.prompts[:3], []string{ask, ask, ask}) || !strings.Contains(r.stderr, "Aiven is now the native default\n") {
		t.Fatalf("exit %d, prompts %q, stderr %q", r.code, p.prompts, r.stderr)
	}
}
