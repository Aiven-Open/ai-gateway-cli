// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package gateway_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Aiven-Open/ai-gateway-cli/internal/gateway"
)

func TestRedirectIsNotFollowedAndKeyNotForwarded(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer other.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/v1/models", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	c := gateway.Client{HTTP: gateway.NewHTTPClient(), BaseURL: redirector.URL}
	_, err := c.Models(context.Background(), "sk-secret")

	var httpErr *gateway.HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusTemporaryRedirect {
		t.Fatalf("err = %v, want HTTPError 307", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("redirect target received %d requests, want 0", hits.Load())
	}
}
