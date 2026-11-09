// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoginStoresKeyAndListsModels(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid + "\n")
	r := s.run("login")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "Key stored in ") || !strings.Contains(r.stdout, ". Models you can use:\n") {
		t.Fatalf("stdout = %q", r.stdout)
	}
	if !strings.Contains(r.stdout, "claude-sonnet ($3.00 in / $15.00 out per 1M tok)\n") {
		t.Fatalf("stdout lacks priced model: %q", r.stdout)
	}
	files := s.homeFiles()
	if len(files) != 1 || filepath.Dir(files[0]) != "aig-keys" {
		t.Fatalf("home files = %v, want one record under aig-keys", files)
	}
	if _, err := os.Stat(filepath.Join(s.env.Home, files[0])); err != nil {
		t.Fatal(err)
	}
}

func TestLoginRecordIsJSONWithCanonicalURLAnd0600(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	s.env.BaseURL = strings.ToUpper(s.env.BaseURL[:7]) + s.env.BaseURL[7:] + "/"
	if r := s.run("login"); r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	path := filepath.Join(s.env.Home, s.homeFiles()[0])
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("record mode = %o, want 600", info.Mode().Perm())
	}
	for _, dir := range []string{s.env.Home, filepath.Dir(path)} {
		d, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if d.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %o, want 700", dir, d.Mode().Perm())
		}
	}
	data, _ := os.ReadFile(path)
	var rec map[string]any
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("record is not JSON: %v", err)
	}
	want := strings.ToLower(strings.TrimSuffix(s.env.BaseURL, "/"))
	if rec["base_url"] != want || rec["key"] != keyValid {
		t.Errorf("record = %v, want base_url %q and the key", rec, want)
	}
	if _, err := time.Parse(time.RFC3339, rec["written_at"].(string)); err != nil {
		t.Errorf("written_at: %v", err)
	}
	for _, optional := range []string{"key_id", "project", "api_url"} {
		if _, ok := rec[optional]; ok {
			t.Errorf("record carries %s; login writes only base_url, key, written_at", optional)
		}
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
	if len(s.homeFiles()) != 1 {
		t.Fatalf("home files = %v", s.homeFiles())
	}
}

func TestLoginRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name, stdin, wantErr string
		wantGatewayCalls     int
	}{
		{"unknown key", "sk-bad\n", "key rejected by gateway (401). Create a key in Aiven Console and run: aiven-llm login", 1},
		{"bad charset", "sk-\"x\n", "that does not look like a gateway key", 0},
		{"inner space", "sk-test valid\n", "that does not look like a gateway key", 0},
		{"empty input", "", "empty key", 0},
		{"blank line", "\n", "empty key", 0},
		{"key without models", keyEmpty + "\n", "key grants no models. Create a key in Aiven Console and run: aiven-llm login", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g).stdin(tc.stdin)
			r := s.run("login")
			if r.code != 1 {
				t.Fatalf("exit %d, want 1", r.code)
			}
			if r.stderr != "aiven-llm: "+tc.wantErr+"\n" {
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

func TestLoginLeavesKeyOnlyInKeystoreRecord(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid + "\n")
	r := s.run("login")
	if strings.Contains(r.stdout+r.stderr, keyValid) {
		t.Errorf("key echoed:\n%s%s", r.stdout, r.stderr)
	}
	if files := s.homeFiles(); len(files) != 1 {
		t.Errorf("home files = %v, want only the record", files)
	}
}

func TestLoginTrimsWhitespaceAroundKey(t *testing.T) {
	for name, in := range map[string]string{"paste tail": keyValid + " \t\n", "windows line ending": keyValid + "\r\n"} {
		t.Run(name, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).stdin(in)
			if r := s.run("login"); r.code != 0 {
				t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
			}
			if rec, err := s.store.Get(s.env.BaseURL); err != nil || rec.Key != keyValid {
				t.Errorf("stored %+v, %v", rec, err)
			}
		})
	}
}
