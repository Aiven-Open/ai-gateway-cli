// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aiven-Open/ai-gateway-cli/internal/gateway"
	"github.com/Aiven-Open/ai-gateway-cli/internal/keystore"
)

func TestModelsPrintsSortedPricesAndNAForUnpriced(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	r := s.run("models")
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	want := "aiven-model (price n/a)\n" +
		"claude-opus ($15.00 in / $75.00 out per 1M tok)\n" +
		"claude-sonnet ($3.00 in / $15.00 out per 1M tok)\n" +
		"glm (price n/a)\n" +
		"gpt-sol ($1.25 in / $10.00 out per 1M tok)\n"
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

func TestModelsWithoutKeyAsksForLogin(t *testing.T) {
	g := newGatewayDouble(t)
	r := newSession(t, g).run("models")
	if r.code != 1 || r.stderr != "aiven-llm: no key stored. Run: aiven-llm login\n" {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if len(g.journal()) != 0 {
		t.Error("contacted gateway without a key")
	}
}

func TestModelsKeyIsPerGatewayURL(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	s.env.BaseURL = "https://other.example"
	r := s.run("models")
	if r.code != 1 || !strings.Contains(r.stderr, "no key stored") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestLogoutRemovesKey(t *testing.T) {
	s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
	r := s.run("logout")
	if r.code != 0 || r.stdout != "key removed\n" {
		t.Fatalf("exit %d, stdout %q", r.code, r.stdout)
	}
	if files := s.homeFiles(); len(files) != 0 {
		t.Errorf("home files = %v, want none", files)
	}
	if r := s.run("models"); r.code != 1 {
		t.Errorf("models after logout exit %d, want 1", r.code)
	}
}

func TestLogoutWithoutKeySucceeds(t *testing.T) {
	r := newSession(t, newGatewayDouble(t)).run("logout")
	if r.code != 0 || r.stdout != "key removed\n" {
		t.Fatalf("exit %d, stdout %q", r.code, r.stdout)
	}
}

func TestGatewayFailuresAreReported(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"server error", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }, "gateway returned HTTP 502"},
		{"forbidden", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }, "gateway returned HTTP 403"},
		{"not json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }, "unexpected response from gateway"},
		{"no data array", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"object":"list"}`)) }, "unexpected response from gateway"},
		{"null data", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"data":null}`)) }, "unexpected response from gateway"},
		{"empty data", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"data":[]}`)) }, "key grants no models. Create a key in Aiven Console and run: aiven-llm login"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			s := newSession(t, &gatewayDouble{Server: srv}).loggedIn(keyValid)
			r := s.run("models")
			if r.code != 1 || r.stderr != "aiven-llm: "+tc.want+"\n" || r.stdout != "" {
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
	url := "http://" + ln.Addr().String()
	_ = ln.Close()
	s := newSession(t, newGatewayDouble(t))
	s.env.BaseURL = url
	s.loggedIn(keyValid)
	r := s.run("models")
	if want := "aiven-llm: cannot reach gateway at " + url + ": "; r.code != 1 || !strings.HasPrefix(r.stderr, want) || !strings.Contains(r.stderr, "refused") {
		t.Fatalf("exit %d, stderr %q, want prefix %q and the dial cause", r.code, r.stderr, want)
	}
}

func TestSlowGatewayTimesOutAsUnreachable(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	s := newSession(t, &gatewayDouble{Server: srv}).loggedIn(keyValid)
	s.deps.HTTP = &http.Client{Timeout: 50 * time.Millisecond}
	r := s.run("models")
	if r.code != 1 || !strings.HasPrefix(r.stderr, "aiven-llm: cannot reach gateway at ") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestDefaultHTTPClientTimesOutAfterTenSeconds(t *testing.T) {
	if got := gateway.NewHTTPClient().Timeout; got != 10*time.Second {
		t.Fatalf("timeout = %v, want 10s", got)
	}
}

// brokenStore is a keystore whose reads fail with a cause other than "no record".
type brokenStore struct{ keystore.Store }

func (brokenStore) Get(string) (keystore.Record, error) {
	return keystore.Record{}, errors.New("keychain did not respond")
}

func TestKeystoreReadFailureIsShownNotMistakenForMissingKey(t *testing.T) {
	s := newSession(t, newGatewayDouble(t))
	s.deps.Keystore = brokenStore{s.store}
	r := s.run("models")
	if r.code != 1 || r.stderr != "aiven-llm: keychain did not respond\n" {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
}
