// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
	"github.com/aiven-open/ai-gateway-cli/internal/gateway"
)

// Redirected output is the same everywhere: one entry per line, no colour, no prices, the router after publishers.
func TestModelsListsByPublisherWithExactIDs(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	r := s.run("models")
	if r.code != 0 || r.stderr != "Gateway: "+s.env.BaseURL+"\n  Retrieving available models…\n  OK 5 models available\n" {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	want := "Anthropic\n  Claude Opus    claude-opus\n  claude-sonnet\n\n" +
		"OpenAI\n  gpt-sol\n\n" +
		"Z.ai\n  glm\n\n" +
		"Aiven routing\n  aiven-model\n"
	if r.stdout != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", r.stdout, want)
	}
}

func TestModelsSendsStoredKeyAsBearer(t *testing.T) {
	g := newGatewayDouble(t)
	newSession(t, g).loggedIn(keyFlash).run("models")
	reqs := g.journal()
	if len(reqs) != 1 || reqs[0].Header.Get("Authorization") != "Bearer "+keyFlash {
		t.Fatalf("requests = %v", reqs)
	}
}

func TestModelsShowsGatewayBeforeAuthenticatedRequest(t *testing.T) {
	g := newGatewayDouble(t)
	s := newSession(t, g).loggedIn(keyValid)
	transport := s.deps.HTTP.Transport
	s.deps.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(s.errb.String(), "Gateway: "+g.URL+"\n") {
			t.Errorf("gateway was not displayed before the authenticated request: %q", s.errb.String())
		}
		return transport.RoundTrip(r)
	})}
	r := s.run("models")
	if r.code != 0 || strings.Contains(r.stdout, "Gateway:") {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}

func TestModelsWithoutKeyAsksForLogin(t *testing.T) {
	g := newGatewayDouble(t)
	r := newSession(t, g).run("models")
	if r.code != 1 || r.stderr != "Gateway: "+g.URL+"\naiven-ai-gateway: no key stored. Run: aiven-ai-gateway login\n" {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if len(g.journal()) != 0 {
		t.Error("contacted gateway without a key")
	}
}

// Another gateway's key file is never read, the default gateway's Console key file included, and neither is the
// environment's key, which may belong to any gateway: without its own key file, nothing is sent.
func TestOnlyTheGatewaysOwnKeyFileIsSent(t *testing.T) {
	for name, origin := range map[string]string{"another gateway": "https://other.example", "default gateway": config.DefaultBaseURL, "no key file": ""} {
		t.Run(name, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g).configured("pi")
			if origin != "" {
				if _, err := creds.Write(s.fakeHome, origin, keyValid, fsutil.WriteAtomic); err != nil {
					t.Fatal(err)
				}
			}
			s.env.Environ = append(s.env.Environ, "AIVEN_AI_GATEWAY_KEY="+keyFlash)
			for _, args := range [][]string{{"models"}, {"launch", "pi"}} {
				if r := s.run(args...); r.code == 0 || !strings.Contains(r.stderr, "no key stored. Run: aiven-ai-gateway login") {
					t.Errorf("%s: exit %d, stderr %q", args[0], r.code, r.stderr)
				}
			}
			if reqs := g.journal(); len(reqs) != 1 {
				t.Errorf("%d requests sent, want only the catalogue read that configured Pi", len(reqs))
			}
			if origin == "" {
				if _, err := os.Stat(filepath.Dir(s.keyPath())); !os.IsNotExist(err) {
					t.Errorf("a read without a key file created the key file's directory: %v", err)
				}
			}
		})
	}
}

func TestLogoutRemovesKey(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	r := s.run("logout")
	if r.code != 0 || r.stdout != "" || r.stderr != "Local key removed. Inherited environment credentials remain; no server key was revoked.\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if files := s.homeFiles(); len(files) != 0 {
		t.Errorf("home files = %v, want none", files)
	}
	if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
		t.Errorf("key file still there: %v", err)
	}
	if r := s.run("models"); r.code != 1 {
		t.Errorf("models after logout exit %d, want 1", r.code)
	}
}

func TestLogoutWithoutKeySucceeds(t *testing.T) {
	r := newSession(t, newGatewayDouble(t)).run("logout")
	if r.code != 0 || r.stdout != "" || r.stderr != "Local key removed. Inherited environment credentials remain; no server key was revoked.\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}

func TestGatewayFailuresAreReported(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"server error", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }, catalogueFailed("the gateway reported a server error", "gateway returned HTTP 502")},
		{"rejected with a full stop", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"message":"Authentication Error."}}`))
		}, catalogueFailed("the gateway rejected the key", "key rejected by gateway (401): Authentication Error. Create a key in Aiven Console and run: aiven-ai-gateway login")},
		{"forbidden", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }, catalogueFailed("the gateway refused the request (HTTP 403)", "gateway returned HTTP 403")},
		{"not json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }, catalogueFailed("the gateway sent an unexpected response", "unexpected response from gateway")},
		{"no data array", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"object":"list"}`)) }, catalogueFailed("the gateway sent an unexpected response", "unexpected response from gateway")},
		{"null data", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"data":null}`)) }, catalogueFailed("the gateway sent an unexpected response", "unexpected response from gateway")},
		{"empty data", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"data":[]}`)) }, catalogueFailed("the key grants no models", "key grants no models. Create a key in Aiven Console and run: aiven-ai-gateway login")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(tc.handler)
			defer srv.Close()
			s := newSession(t, &gatewayDouble{Server: srv}).loggedIn(keyValid)
			r := s.run("models")
			if r.code != 1 || r.stderr != "Gateway: "+srv.URL+"\n"+tc.want || r.stdout != "" {
				t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
		})
	}
}

func TestUnreachableGatewayNamesURL(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "https://" + ln.Addr().String()
	_ = ln.Close()
	s := newSession(t, newGatewayDouble(t))
	s.env.BaseURL = url
	s.loggedIn(keyValid)
	r := s.run("models")
	if want := "Gateway: " + url + "\n" + catalogueFailed("the gateway could not be reached", "cannot reach gateway at "+url+": "); r.code != 1 || !strings.HasPrefix(r.stderr, strings.TrimSuffix(want, "\n")) || !strings.Contains(r.stderr, "refused") {
		t.Fatalf("exit %d, stderr %q, want prefix %q and the dial cause", r.code, r.stderr, want)
	}
}

func TestSlowGatewayTimesOutAsUnreachable(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	s := newSession(t, &gatewayDouble{Server: srv}).loggedIn(keyValid)
	client := *srv.Client()
	client.Timeout = 50 * time.Millisecond
	s.deps.HTTP = &client
	r := s.run("models")
	if want := "Gateway: " + srv.URL + "\n" + catalogueFailed("the gateway could not be reached", "cannot reach gateway at "); r.code != 1 || !strings.HasPrefix(r.stderr, strings.TrimSuffix(want, "\n")) {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestDefaultHTTPClientTimesOutAfterTenSeconds(t *testing.T) {
	if got := gateway.NewHTTPClient().Timeout; got != 10*time.Second {
		t.Fatalf("timeout = %v, want 10s", got)
	}
}

func TestKeyFileReadFailureIsShownNotMistakenForMissingKey(t *testing.T) {
	s := newSession(t, newGatewayDouble(t))
	if err := os.MkdirAll(s.keyPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	r := s.run("models")
	if r.code != 1 || !strings.HasPrefix(r.stderr, "Gateway: "+s.env.BaseURL+"\naiven-ai-gateway: read the key file: ") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
}
