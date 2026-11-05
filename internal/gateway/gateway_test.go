// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package gateway_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/gateway"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

func TestModelsContextWindowFallsBackToMaxInput(t *testing.T) {
	cases := []struct {
		name, fields string
		context      *int
	}{
		{"context window alone", `"context_window_tokens":262144.9`, ptr(262144)},
		{"max input alone", `"max_input_tokens":163840`, ptr(163840)},
		{"null context window", `"context_window_tokens":null,"max_input_tokens":163840`, ptr(163840)},
		{"zero context window falls back", `"context_window_tokens":0,"max_input_tokens":163840`, ptr(163840)},
		{"negative context window falls back", `"context_window_tokens":-100,"max_input_tokens":163840`, ptr(163840)},
		{"context window wins", `"context_window_tokens":1000000,"max_input_tokens":200000`, ptr(1000000)},
		{"zero ignored", `"max_input_tokens":0`, nil},
		{"negative ignored", `"context_window_tokens":-5,"max_input_tokens":-1`, nil},
		{"neither", `"object":"model"`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet",` + tc.fields + `}]}`))
			}))
			defer srv.Close()
			ms, err := gateway.Client{HTTP: gateway.NewHTTPClient(), BaseURL: srv.URL}.Models(context.Background(), "sk")
			if err != nil {
				t.Fatal(err)
			}
			if got := ms[0]; !reflect.DeepEqual(got.Context, tc.context) {
				t.Errorf("Context = %v, want %v", deref(got.Context), deref(tc.context))
			}
		})
	}
}

func ptr(n int) *int { return &n }

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestRedirectIsNotFollowedAndKeyNotForwarded(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer other.Close()
	redirector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/v1/models", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	c := gateway.Client{HTTP: redirector.Client(), BaseURL: redirector.URL}
	_, err := c.Models(context.Background(), "sk-secret")

	var httpErr *gateway.HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusTemporaryRedirect {
		t.Fatalf("err = %v, want HTTPError 307", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("redirect target received %d requests, want 0", hits.Load())
	}
}

func TestProbeSendsOneTokenCompletionAndMapsStatus(t *testing.T) {
	cases := []struct {
		status int
		check  func(error) bool
	}{
		{200, func(err error) bool { return err == nil }},
		{401, func(err error) bool { return errors.Is(err, gateway.ErrUnauthorized) }},
		{500, func(err error) bool { var h *gateway.HTTPError; return errors.As(err, &h) && h.Status == 500 }},
		{302, func(err error) bool { var h *gateway.HTTPError; return errors.As(err, &h) && h.Status == 302 }},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				got = r.Method + " " + r.URL.Path + " " + r.Header.Get("Authorization") + " " + string(body)
				w.Header().Set("Location", "/elsewhere")
				w.WriteHeader(tc.status)
				if tc.status == 200 {
					_, _ = w.Write([]byte(`{"choices":[]}`))
				}
			}))
			defer srv.Close()
			_, err := gateway.Client{HTTP: gateway.NewHTTPClient(), BaseURL: srv.URL}.Probe(context.Background(), "sk", "m\"1", models.ChatCompletions)
			if !tc.check(err) {
				t.Fatalf("err = %v", err)
			}
			want := `POST /v1/chat/completions Bearer sk {"max_tokens":1,"messages":[{"content":"hi","role":"user"}],"model":"m\"1"}`
			if got != want {
				t.Errorf("request = %s, want %s", got, want)
			}
		})
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	var unreachable *gateway.UnreachableError
	if _, err := (gateway.Client{HTTP: gateway.NewHTTPClient(), BaseURL: srv.URL}).Probe(context.Background(), "sk", "m", models.ChatCompletions); !errors.As(err, &unreachable) {
		t.Fatalf("closed server: err = %v, want UnreachableError", err)
	}
}

func TestModelsDecodesMaxOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet","max_output_tokens":64000.9},{"id":"gpt-sol","max_output_tokens":0},{"id":"glm"}]}`))
	}))
	defer srv.Close()
	ms, err := gateway.Client{HTTP: gateway.NewHTTPClient(), BaseURL: srv.URL}.Models(context.Background(), "sk")
	if err != nil || !reflect.DeepEqual(ms[0].MaxOutput, ptr(64000)) || ms[1].MaxOutput != nil || ms[2].MaxOutput != nil {
		t.Fatalf("err = %v, MaxOutputs = %v %v %v", err, deref(ms[0].MaxOutput), deref(ms[1].MaxOutput), deref(ms[2].MaxOutput))
	}
}

// Owner and display name are decoded verbatim apart from what could move or recolour a terminal.
func TestModelsDecodesOwnerAndDisplayNameWithoutControlCharacters(t *testing.T) {
	cases := []struct{ name, fields, owner, display string }{
		{"absent", `"object":"model"`, "", ""},
		{"plain", `"owned_by":"Anthropic","display_name":"Claude Sonnet 5.5"`, "Anthropic", "Claude Sonnet 5.5"},
		{"escape sequences", `"owned_by":"\u001b[31mEvil","display_name":"Name\u001b]0;title\u0007\r\n"`, "[31mEvil", "Name]0;title"},
		{"bidi override", `"display_name":"abc\u202edcba"`, "", "abcdcba"},
		{"padding", `"owned_by":"  zai  "`, "zai", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet",` + tc.fields + `}]}`))
			}))
			defer srv.Close()
			ms, err := gateway.Client{HTTP: gateway.NewHTTPClient(), BaseURL: srv.URL}.Models(context.Background(), "sk")
			if err != nil {
				t.Fatal(err)
			}
			if ms[0].Owner != tc.owner || ms[0].Name != tc.display {
				t.Errorf("owner %q, name %q; want %q, %q", ms[0].Owner, ms[0].Name, tc.owner, tc.display)
			}
		})
	}
}
