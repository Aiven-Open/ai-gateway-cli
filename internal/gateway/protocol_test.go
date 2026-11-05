// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/gateway"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

func TestBasicChecksValidateResponsesAndRetainSafeGatewayErrors(t *testing.T) {
	t.Parallel()
	const key = "synthetic-secret-000000000"
	for _, tc := range []struct {
		name, body      string
		status          int
		protocol        models.Protocol
		wantError, auth bool
		message         string
	}{
		{"chat", `{"choices":[]}`, 200, models.ChatCompletions, false, false, ""},
		{"messages", `{"content":[]}`, 200, models.Messages, false, false, ""},
		{"responses", `{"output":[]}`, 200, models.Responses, false, false, ""},
		{"completed responses with null error", `{"id":"resp_synthetic","object":"response","status":"completed","error":null,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi","annotations":[]}]}]}`, 200, models.Responses, false, false, ""},
		{"null error with wrong envelope", `{"error":null,"choices":[]}`, 200, models.Responses, true, false, ""},
		{"wrong envelope", `{"choices":[]}`, 200, models.Responses, true, false, ""},
		{"empty", ``, 200, models.Messages, true, false, ""},
		{"truncated", `{"content":[`, 200, models.Messages, true, false, ""},
		{"null", `{"content":null}`, 200, models.Messages, true, false, ""},
		{"error in success", `{"error":{"message":"store not accepted"}}`, 200, models.ChatCompletions, true, false, "store not accepted"},
		{"nested request refusal", `{"error":{"code":"bad_argument","message":"Unrecognized request argument supplied: store"}}`, 400, models.ChatCompletions, true, false, "bad_argument: Unrecognized request argument supplied: store"},
		{"top-level request refusal", `{"code":"bad_argument","message":"store not accepted"}`, 400, models.ChatCompletions, true, false, "store not accepted"},
		{"string request refusal", `{"error":"store is not supported"}`, 400, models.ChatCompletions, true, false, "store is not supported"},
		{"string error in success", `{"error":"store is not supported","output":[]}`, 200, models.Responses, true, false, "store is not supported"},
		{"string reflected credentials", `{"error":"store ` + key + ` \u001b[31m sk-other-secret password=hidden"}`, 400, models.ChatCompletions, true, false, "store"},
		{"authentication", `{"error":{"message":"expired"}}`, 401, models.Messages, true, true, "expired"},
		{"permission", `{"error":{"message":"model denied"}}`, 403, models.Messages, true, false, "model denied"},
		{"rate limit", `{"message":"rate limit"}`, 429, models.Responses, true, false, "rate limit"},
		{"server", `{"message":"unavailable"}`, 503, models.Responses, true, false, "unavailable"},
		{"reflected credentials", `{"message":"store ` + key + ` \u001b[31m sk-other-secret AVNS_anotherSECRET password=hidden"}`, 400, models.ChatCompletions, true, false, "store"},
		{"split credential", `{"message":"store sk-split\u0007-secret"}`, 400, models.ChatCompletions, true, false, "store"},
		{"word ending in sk", `{"message":"no model risk-scorer-v2"}`, 404, models.ChatCompletions, true, false, "no model risk-scorer-v2"},
		{"long", `{"message":"` + strings.Repeat("x", 2048) + `"}`, 400, models.ChatCompletions, true, false, "xxx"},
		{"oversized invalid", strings.Repeat("x", 5000), 400, models.ChatCompletions, true, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+key {
					t.Error("missing authorization")
				}
				if tc.protocol == models.Messages && r.Header.Get("anthropic-version") != "2023-06-01" {
					t.Error("missing Messages version")
				}
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			status, err := (gateway.Client{HTTP: gateway.NewHTTPClient(), BaseURL: srv.URL}).Probe(t.Context(), key, "model", tc.protocol)
			if status != tc.status || (err != nil) != tc.wantError || errors.Is(err, gateway.ErrUnauthorized) != tc.auth {
				t.Fatalf("status=%d err=%v", status, err)
			}
			if err == nil {
				return
			}
			text := err.Error()
			if !strings.Contains(text, tc.message) || len([]rune(text)) > 600 {
				t.Errorf("missing meaningful bounded diagnostic: %q", text)
			}
			for _, secret := range []string{key, "\x1b", "sk-other-secret", "AVNS_anotherSECRET", "password=hidden", "-secret"} {
				if strings.Contains(text, secret) {
					t.Errorf("unsafe diagnostic contains %q", secret)
				}
			}
		})
	}
}

// Each credential form an upstream error can reflect is hidden, and only its value.
func TestGatewayErrorsHideReflectedCredentials(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ message, want string }{
		{`upstream {"apiKey":"opaque-value"}`, `upstream {"apiKey":[redacted]}`},
		{`upstream 'api_key': 'opaque-value'`, `upstream 'api_key': [redacted]`},
		{`upstream cookie="opaque-value"`, `upstream cookie=[redacted]`},
		{`upstream api_key="opaque value with spaces" rest`, `upstream api_key=[redacted] rest`},
		{`upstream Authorization: Bearer opaque.value`, `upstream Authorization: [redacted]`},
		{`upstream bearer opaque.value`, `upstream bearer [redacted]`},
		{`upstream GITHUB_PAT=opaque-value`, `upstream GITHUB_PAT=[redacted]`},
		{`upstream ghp_0123456789abcdefghijABCD and AKIA0123456789ABCDEF`, `upstream [redacted] and [redacted]`},
		{`upstream AVNS_ab.c-d_e and sk-0123456789.ab`, `upstream [redacted] and [redacted]`},
		{`no model path-v2, pattern: x`, `no model path-v2, pattern: x`},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": tc.message})
		}))
		_, err := (gateway.Client{HTTP: gateway.NewHTTPClient(), BaseURL: srv.URL}).Probe(t.Context(), "synthetic-request-key", "model", models.Messages)
		srv.Close()
		if want := "gateway returned HTTP 400: " + tc.want; err == nil || err.Error() != want {
			t.Errorf("%s: %v, want %q", tc.message, err, want)
		}
	}
}

func TestBasicCheckCancellationIsNotAuthenticationFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	status, err := (gateway.Client{HTTP: gateway.NewHTTPClient(), BaseURL: "http://127.0.0.1:1"}).Probe(ctx, "synthetic", "m", models.Messages)
	if status != 0 || !errors.Is(err, context.Canceled) || errors.Is(err, gateway.ErrUnauthorized) {
		t.Fatalf("status=%d err=%v", status, err)
	}
}

func TestListedNonPublicModelIsAdmittedAndRoutedToChat(t *testing.T) {
	t.Parallel()
	private := "private-customer-model"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"data":[{"id":%q,"display_name":"Private model"},{"id":"deepseek-v3.2"}]}`, private)
	}))
	defer srv.Close()
	table, err := (gateway.Client{HTTP: gateway.NewHTTPClient(), BaseURL: srv.URL}).Models(t.Context(), "synthetic")
	if err != nil || len(table) != 2 || table[0].Alias != private {
		t.Fatalf("listed model was not admitted: count=%d err=%v", len(table), err)
	}
	if protocol := models.ModelProtocol(table[0].Alias); protocol != models.ChatCompletions {
		t.Fatalf("listed non-public model route: %v", protocol)
	}
}
