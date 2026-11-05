// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

func TestLoginStoresKeyAndListsModels(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid + "\n")
	r := s.run("login")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "OK saved to ") || !strings.Contains(r.stderr, "no inference request was sent") || strings.Contains(r.stderr, "Sending one test request") {
		t.Fatalf("stderr = %q", r.stderr)
	}
	if !strings.Contains(r.stdout, "Anthropic\n  Claude Opus    claude-opus\n  claude-sonnet\n") || strings.Contains(r.stdout, "$") {
		t.Fatalf("stdout lacks the grouped catalogue: %q", r.stdout)
	}
	path := filepath.Join(s.fakeHome, ".config", "aiven", "ai-gateway-keys", creds.Name(s.env.BaseURL))
	if !strings.Contains(r.stderr, "OK saved to "+path+"\n") {
		t.Errorf("stderr lacks the key file path: %q", r.stderr)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != keyValid+"\n" {
		t.Fatalf("key file = %q, %v", data, err)
	}
	if info, _ := os.Stat(path); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("key file mode = %v, want 0600", info.Mode().Perm())
	}
	// The gateway double is not the default gateway, so the Console's key file is not this one.
	if _, err := os.Stat(filepath.Join(s.fakeHome, ".config/aiven/ai-gateway-key")); !os.IsNotExist(err) {
		t.Errorf("the default gateway's key file was written: %v", err)
	}
}

func TestLoginCreatesKeyDirectoriesPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permission bits")
	}
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	if r := s.run("login"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	for _, dir := range []string{filepath.Join(s.fakeHome, ".config"), filepath.Join(s.fakeHome, ".config/aiven"), filepath.Dir(s.keyPath())} {
		if d, err := os.Stat(dir); err != nil || d.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v, %v; want mode 700", dir, d, err)
		}
	}
}

func TestLoginKeepsTheModeOfAnExistingConfigDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permission bits")
	}
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	dir := filepath.Join(s.fakeHome, ".config/aiven")
	// Chmod, unlike MkdirAll, ignores the umask.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	} else if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if r := s.run("login"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if d, _ := os.Stat(dir); d.Mode().Perm() != 0o755 {
		t.Errorf("%s mode = %o, want 755 kept", dir, d.Mode().Perm())
	}
}

func TestLoginSendsKeyAsBearerAndNowhereElse(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).stdin(keyValid + "\n")
	s.run("login")
	reqs := g.journal()
	if len(reqs) != 1 {
		t.Fatalf("%d gateway requests, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Method != "GET" || r.URL.Path != "/v1/models" || r.URL.RawQuery != "" {
		t.Errorf("request = %s %s", r.Method, r.URL)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+keyValid {
		t.Errorf("Authorization = %q", got)
	}
}

func TestLoginAcceptsKeyWithoutTrailingNewline(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	if r := s.run("login"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if files := s.homeFiles(); len(files) != 0 {
		t.Fatalf("home files = %v", files)
	}
}

func TestLoginRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name, stdin, wantErr string
		wantGatewayCalls     int
	}{
		{"unknown key", "sk-bad\n", "the gateway rejected the key\naiven-ai-gateway: key rejected by gateway (401): Authentication Error. Create a key in Aiven Console and run: aiven-ai-gateway login", 1},
		{"bad charset", "sk-\"x\n", "that does not look like a gateway key", 0},
		{"inner space", "sk-test valid\n", "that does not look like a gateway key", 0},
		{"empty input", "", "empty key", 0},
		{"blank line", "\n", "empty key", 0},
		{"key without models", keyEmpty + "\n", "the key grants no models\naiven-ai-gateway: key grants no models. Create a key in Aiven Console and run: aiven-ai-gateway login", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g).stdin(tc.stdin)
			r := s.run("login")
			if r.code != 1 {
				t.Fatalf("exit %d, want 1", r.code)
			}
			want := "aiven-ai-gateway: " + tc.wantErr + "\n"
			if tc.wantGatewayCalls > 0 {
				want = "  Retrieving available models…\n  FAILED " + tc.wantErr + "\n"
			}
			want = "Gateway: " + g.URL + "\n" + want
			if r.stderr != want {
				t.Errorf("stderr = %q", r.stderr)
			}
			if r.stdout != "" {
				t.Errorf("stdout = %q, want empty", r.stdout)
			}
			if len(s.homeFiles()) != 0 {
				t.Errorf("wrote %v, want nothing", s.homeFiles())
			}
			if n := len(g.journal()); n != tc.wantGatewayCalls {
				t.Errorf("%d gateway calls, want %d", n, tc.wantGatewayCalls)
			}
		})
	}
}

type fakePrompter struct{ prompt, secret string }

func (p *fakePrompter) ReadSecret(prompt string) (string, error) {
	p.prompt = prompt
	return p.secret, nil
}

func (p *fakePrompter) ReadLine(string) (string, error) { return "", io.EOF }

func TestLoginOnTerminalReadsKeyFromPrompter(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin("sk-ignored\n")
	p := &fakePrompter{secret: keyValid}
	s.deps.Prompter = p
	if r := s.run("login"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if p.prompt != "Paste your gateway key: " {
		t.Errorf("prompt = %q", p.prompt)
	}
}

func TestLoginLeavesKeyOnlyInTheKeyFile(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid + "\n")
	r := s.run("login")
	if strings.Contains(r.stdout+r.stderr, keyValid) {
		t.Errorf("key echoed:\n%s%s", r.stdout, r.stderr)
	}
	if files := s.homeFiles(); len(files) != 0 {
		t.Errorf("home files = %v, want none", files)
	}
}

func TestLoginTrimsWhitespaceAroundKey(t *testing.T) {
	for name, in := range map[string]string{"paste tail": keyValid + " \t\n", "windows line ending": keyValid + "\r\n"} {
		t.Run(name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).stdin(in)
			if r := s.run("login"); r.code != 0 {
				t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
			}
			if data, err := os.ReadFile(s.keyPath()); err != nil || string(data) != keyValid+"\n" {
				t.Errorf("key file = %q, %v", data, err)
			}
		})
	}
}

// Each gateway keeps its own key file, so a login to another gateway leaves this one's key, and each sends its own.
func TestLoginToAnotherGatewayKeepsThisGatewaysKey(t *testing.T) {
	a := newGatewayDouble(t)
	s := newSession(t, a).loggedIn(keyValid)
	aKey := s.keyPath()
	b := newGatewayDouble(t)
	s.env.BaseURL = b.URL
	s.deps.HTTP = b.Client()
	if r := s.stdin(keyFlash).run("login"); r.code != 0 {
		t.Fatalf("login to b: exit %d, stderr %q", r.code, r.stderr)
	}
	if aKey == s.keyPath() || readOrEmpty(t, aKey) != keyValid+"\n" || readOrEmpty(t, s.keyPath()) != keyFlash+"\n" {
		t.Fatalf("key files: a %q, b %q", readOrEmpty(t, aKey), readOrEmpty(t, s.keyPath()))
	}
	for _, g := range []*gatewayDouble{a, b} {
		s.env.BaseURL = g.URL
		s.deps.HTTP = g.Client()
		if r := s.run("models"); r.code != 0 {
			t.Errorf("models on %s: exit %d, stderr %q", g.URL, r.code, r.stderr)
		}
	}
	for g, want := range map[*gatewayDouble]string{a: keyValid, b: keyFlash} {
		for _, req := range g.journal() {
			if got := req.Header.Get("Authorization"); got != "Bearer "+want {
				t.Errorf("%s %s sent %q, want its own key", g.URL, req.URL.Path, got)
			}
		}
	}
	s.env.BaseURL = b.URL
	if r := s.run("logout"); r.code != 0 {
		t.Fatalf("logout from b: exit %d, stderr %q", r.code, r.stderr)
	}
	if readOrEmpty(t, aKey) != keyValid+"\n" || readOrEmpty(t, s.keyPath()) != "" {
		t.Errorf("after logout from b: a %q, b %q", readOrEmpty(t, aKey), readOrEmpty(t, s.keyPath()))
	}
}

// failNth makes the nth write to path fail and lets every other write through.
func failNth(path string, n int) func(string, []byte, os.FileMode) error {
	seen := 0
	return func(p string, data []byte, perm os.FileMode) error {
		if p == path {
			if seen++; seen == n {
				return errors.New("disk full")
			}
		}
		return fsutil.WriteAtomic(p, data, perm)
	}
}

// A failed save keeps the key from before it: the key file is replaced in one rename or not at all.
func TestLoginKeepsTheSavedKeyWhenASaveFails(t *testing.T) {
	for name, saved := range map[string]string{"replacing a key": keyFlash, "first key": ""} {
		t.Run(name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t))
			if saved != "" {
				s.loggedIn(saved)
			}
			s.deps.WriteFile = failNth(s.keyPath(), 1)
			if r := s.stdin(keyValid).run("login"); r.code != 1 || !strings.Contains(r.stderr, "key not saved: ") {
				t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
			}
			want := ""
			if saved != "" {
				want = saved + "\n"
			}
			if got := readOrEmpty(t, s.keyPath()); got != want {
				t.Errorf("key file = %q, want %q", got, want)
			}
		})
	}
}

func readOrEmpty(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func TestLoginPointsToSetupBeforeLaunching(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	r := s.run("login")
	if r.code != 0 || !strings.Contains(r.stderr, "no inference request was sent and no tool") || !strings.Contains(r.stderr, "aiven-ai-gateway setup <harness>") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
}
