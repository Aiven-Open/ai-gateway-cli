// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
)

type consentPrompter struct {
	answers     []string
	secret      string
	secretErr   error
	lineErr     error
	prompts     []string
	secretCalls int
	onLine      func(string)
	onSecret    func()
}

func (p *consentPrompter) ReadLine(prompt string) (string, error) {
	p.prompts = append(p.prompts, prompt)
	if p.onLine != nil {
		p.onLine(prompt)
	}
	if p.lineErr != nil {
		return "", p.lineErr
	}
	if len(p.answers) == 0 {
		return "", io.EOF
	}
	answer := p.answers[0]
	p.answers = p.answers[1:]
	return answer, nil
}

func (p *consentPrompter) ReadSecret(string) (string, error) {
	p.secretCalls++
	if p.onSecret != nil {
		p.onSecret()
	}
	return p.secret, p.secretErr
}

func TestSetupEnvironmentKeyConsentDecisions(t *testing.T) {
	for _, tc := range []struct {
		name, saved, secret, wantKey string
		answers                      []string
		lineErr, secretErr           error
		wantSecretCalls              int
	}{
		{name: "approve", answers: []string{"y"}, wantKey: keyLong},
		{name: "invalid then approve", answers: []string{"maybe", "YES"}, wantKey: keyLong},
		{name: "decline", answers: []string{"n"}, secret: keyValid, wantKey: keyValid, wantSecretCalls: 1},
		{name: "enter declines", answers: []string{""}, secret: keyValid, wantKey: keyValid, wantSecretCalls: 1},
		{name: "input ends"},
		{name: "input cancelled", lineErr: context.Canceled},
		{name: "invalid answers", answers: []string{"?", "maybe", "later"}},
		{name: "decline then input ends", answers: []string{"no"}, secretErr: io.EOF, wantSecretCalls: 1},
		{name: "rejected saved then approve", saved: "sk-expired", answers: []string{"y"}, wantKey: keyLong},
		{name: "no-model saved then approve", saved: keyEmpty, answers: []string{"y"}, wantKey: keyLong},
		{name: "rejected saved then input ends", saved: "sk-expired"},
		{name: "no-model saved then decline and input ends", saved: keyEmpty, answers: []string{"n"}, secretErr: io.EOF, wantSecretCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g)
			s.env.Environ = append(s.env.Environ, creds.EnvVar+"="+keyLong)
			writeFile(t, filepath.Join(s.fakeHome, ".omp/agent/models.yml"), "# existing\nproviders: {}\n")
			if tc.saved != "" {
				s.loggedIn(tc.saved)
			}
			before := configurationSnapshot(t, s.fakeHome, s.env.WorkDir)
			expectedBefore := 0
			if tc.saved != "" {
				expectedBefore = 1
			}
			checkBefore := func() {
				t.Helper()
				reqs := g.journal()
				if len(reqs) != expectedBefore {
					t.Fatalf("requests before consent or replacement = %d, want %d", len(reqs), expectedBefore)
				}
				for _, req := range reqs {
					if req.Header.Get("Authorization") != "Bearer "+tc.saved {
						t.Fatal("a different credential was sent before consent")
					}
				}
				if after := configurationSnapshot(t, s.fakeHome, s.env.WorkDir); !reflect.DeepEqual(before, after) {
					t.Fatal("files changed before consent or replacement input")
				}
			}
			p := &consentPrompter{answers: tc.answers, secret: tc.secret, lineErr: tc.lineErr, secretErr: tc.secretErr}
			p.onLine = func(prompt string) {
				if want := "Use this key with " + g.URL + "? [y/N] "; prompt != want {
					t.Errorf("prompt = %q, want %q", prompt, want)
				}
				for _, want := range []string{"Gateway: " + g.URL, "Key source: " + creds.EnvVar, "Key: AVNS_…wxyz"} {
					if !strings.Contains(s.errb.String(), want) {
						t.Errorf("before prompt, stderr lacks %q", want)
					}
				}
				checkBefore()
			}
			p.onSecret = checkBefore
			s.deps.Prompter = p
			r := s.run("setup", "omp", "--yes")
			if p.secretCalls != tc.wantSecretCalls || len(p.prompts) == 0 {
				t.Fatalf("prompts %q, secret calls %d", p.prompts, p.secretCalls)
			}
			if strings.Contains(r.stdout+r.stderr, keyLong) || r.stdout != "" {
				t.Fatal("credential or non-setup output reached a stream")
			}
			if tc.wantKey == "" {
				if r.code != 1 {
					t.Fatalf("cancelled setup exit %d: %s", r.code, r.stderr)
				}
				checkBefore()
				return
			}
			if r.code != 0 || readOrEmpty(t, s.keyPath()) != tc.wantKey+"\n" {
				t.Fatalf("exit %d or wrong saved key: %s", r.code, r.stderr)
			}
			reqs := g.journal()
			if len(reqs) <= expectedBefore || reqs[expectedBefore].Header.Get("Authorization") != "Bearer "+tc.wantKey {
				t.Fatal("the selected key was not used after its prompt")
			}
			for _, req := range reqs[expectedBefore:] {
				if req.Header.Get("Authorization") != "Bearer "+tc.wantKey {
					t.Fatal("setup switched to another credential")
				}
			}
		})
	}
}

func TestSetupEnvironmentKeyMasksShortValues(t *testing.T) {
	for _, tc := range []struct{ key, mask string }{
		{keyShort, "sk-…"},
		{keyBare, "…"},
		{keyLong, "AVNS_…wxyz"},
	} {
		t.Run(tc.mask, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g)
			s.env.Environ = append(s.env.Environ, creds.EnvVar+"="+tc.key)
			p := &consentPrompter{}
			s.deps.Prompter = p
			r := s.run("setup", "omp", "--yes")
			if r.code != 1 || !strings.Contains(r.stderr, "Key: "+tc.mask+"\n") ||
				strings.Contains(r.stderr, tc.key) || len(g.journal()) != 0 {
				t.Fatalf("exit %d, stderr %q, requests %d", r.code, r.stderr, len(g.journal()))
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSetupConfirmsEnvironmentKeyForHostedDefault(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g)
	env, err := config.Load([]string{config.HomeVar + "=" + s.fakeHome, creds.EnvVar + "=" + keyLong})
	if err != nil {
		t.Fatal(err)
	}
	env.WorkDir = s.env.WorkDir
	s.env = env
	target, err := url.Parse(g.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := g.Client().Transport
	s.deps.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme+"://"+r.URL.Host != config.DefaultBaseURL {
			t.Errorf("request origin = %s", r.URL)
		}
		clone := r.Clone(r.Context())
		clone.URL.Scheme, clone.URL.Host, clone.Host = target.Scheme, target.Host, target.Host
		return transport.RoundTrip(clone)
	})}
	p := &consentPrompter{answers: []string{"y"}}
	p.onLine = func(prompt string) {
		if prompt != "Use this key with "+config.DefaultBaseURL+"? [y/N] " || len(g.journal()) != 0 {
			t.Fatalf("prompt %q or requests before consent", prompt)
		}
	}
	s.deps.Prompter = p
	r := s.run("setup", "omp", "--yes")
	if r.code != 0 || !strings.Contains(r.stderr, "Gateway: "+config.DefaultBaseURL+"\n") ||
		readOrEmpty(t, s.keyPath()) != keyLong+"\n" || len(g.journal()) == 0 {
		t.Fatalf("hosted default setup: exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestUnattendedSetupDoesNotUseEnvironmentKey(t *testing.T) {
	for _, saved := range []string{"", "sk-expired", keyEmpty} {
		for _, yes := range []bool{false, true} {
			t.Run(saved+map[bool]string{false: "/without-yes", true: "/with-yes"}[yes], func(t *testing.T) {
				g := newGatewayDouble(t)
				s := newSession(t, g).stdin(keyValid)
				s.env.Environ = append(s.env.Environ, creds.EnvVar+"="+keyLong)
				if saved != "" {
					s.loggedIn(saved)
				}
				before := configurationSnapshot(t, s.fakeHome)
				args := []string{"setup", "omp"}
				if yes {
					args = append(args, "--yes")
				}
				r := s.run(args...)
				if r.code != 1 || !strings.Contains(r.stderr, "--key-stdin") || r.stdout != "" {
					t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
				}
				if after := configurationSnapshot(t, s.fakeHome); !reflect.DeepEqual(before, after) {
					t.Fatal("unattended setup changed files")
				}
				for _, req := range g.journal() {
					if req.Header.Get("Authorization") != "Bearer "+saved || saved == "" {
						t.Fatal("unattended setup sent the environment key")
					}
				}
			})
		}
	}
}

func TestStdinOverridesSavedAndEnvironmentKeysUnattended(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyFlash).stdin(keyValid)
	s.env.Environ = append(s.env.Environ, creds.EnvVar+"="+keyLong)
	r := s.run("setup", "omp", "--key-stdin", "--yes")
	if r.code != 0 || r.stdout != "" || readOrEmpty(t, s.keyPath()) != keyValid+"\n" {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	for _, req := range g.journal() {
		if req.Header.Get("Authorization") != "Bearer "+keyValid {
			t.Fatal("explicit stdin did not override another credential source")
		}
	}
	if len(g.journal()) < 2 {
		t.Fatal("setup did not perform its catalogue and protocol checks")
	}
}

func TestSavedKeyReuseDoesNotSendEnvironmentKey(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	s.env.Environ = append(s.env.Environ, creds.EnvVar+"="+keyLong)
	r := s.run("setup", "omp", "--yes")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	for _, req := range g.journal() {
		if req.Header.Get("Authorization") != "Bearer "+keyValid {
			t.Fatal("setup sent a key other than the saved key")
		}
	}
}

func TestSavedKeyRequestFailureDoesNotSwitchToEnvironment(t *testing.T) {
	for _, failure := range []string{"server", "transport"} {
		t.Run(failure, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g).loggedIn(keyValid)
			s.env.Environ = append(s.env.Environ, creds.EnvVar+"="+keyLong)
			before := configurationSnapshot(t, s.fakeHome)
			var calls atomic.Int32
			if failure == "server" {
				g.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Header.Get("Authorization") != "Bearer "+keyValid {
						t.Error("server received a different key")
					}
					w.WriteHeader(http.StatusBadGateway)
				})
			} else {
				s.deps.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls.Add(1)
					if r.Header.Get("Authorization") != "Bearer "+keyValid {
						t.Error("transport received a different key")
					}
					return nil, errors.New("synthetic transport failure")
				})}
			}
			p := &consentPrompter{answers: []string{"y"}}
			s.deps.Prompter = p
			r := s.run("setup", "omp", "--yes")
			if r.code != 1 || calls.Load() != 1 || len(p.prompts) != 0 {
				t.Fatalf("exit %d, calls %d, prompts %q: %s", r.code, calls.Load(), p.prompts, r.stderr)
			}
			if after := configurationSnapshot(t, s.fakeHome); !reflect.DeepEqual(before, after) {
				t.Fatal("request failure changed files")
			}
		})
	}
}

func TestLoginShowsGatewayBeforeExplicitInput(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g)
	s.env.Environ = append(s.env.Environ, creds.EnvVar+"="+keyLong)
	p := &consentPrompter{secret: keyValid}
	p.onSecret = func() {
		if !strings.Contains(s.errb.String(), "Gateway: "+g.URL+"\n") || len(g.journal()) != 0 {
			t.Fatal("login did not show the gateway before input")
		}
	}
	s.deps.Prompter = p
	r := s.run("login")
	if r.code != 0 || len(p.prompts) != 0 || readOrEmpty(t, s.keyPath()) != keyValid+"\n" {
		t.Fatalf("exit %d, prompts %q: %s", r.code, p.prompts, r.stderr)
	}
	for _, req := range g.journal() {
		if req.Header.Get("Authorization") != "Bearer "+keyValid {
			t.Fatal("login used the environment key")
		}
	}
}
