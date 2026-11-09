// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package gateway reads the key's model table from AI Gateway `GET /v1/models`.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Aiven-Open/ai-gateway-cli/internal/models"
)

// Timeout bounds every gateway call.
const Timeout = 10 * time.Second

const maxBody = 10 << 20

var (
	// ErrUnauthorized is a 401: the key is unknown, revoked or expired.
	ErrUnauthorized = errors.New("key rejected by gateway (401)")
	// ErrNoModels means the key is valid but grants nothing.
	ErrNoModels = errors.New("key grants no models")
	// ErrUnexpected means a 2xx body without a `data` array.
	ErrUnexpected = errors.New("unexpected response from gateway")
)

// UnreachableError is a transport failure (DNS, refused, timeout).
type UnreachableError struct {
	BaseURL string
	Err     error
}

func (e *UnreachableError) Error() string {
	cause := e.Err
	if ue := new(url.Error); errors.As(cause, &ue) {
		cause = ue.Err // the request URL is already in the message
	}
	return "cannot reach gateway at " + e.BaseURL + ": " + cause.Error()
}
func (e *UnreachableError) Unwrap() error { return e.Err }

// HTTPError is any other non-2xx status.
type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("gateway returned HTTP %d", e.Status) }

// NewHTTPClient returns the client the launcher uses unless a caller injects one.
func NewHTTPClient() *http.Client { return &http.Client{Timeout: Timeout} }

// Client talks to one gateway.
type Client struct {
	HTTP    *http.Client
	BaseURL string
}

type modelEntry struct {
	ID       string   `json:"id"`
	Context  *float64 `json:"context_window_tokens"`
	InUSD1M  *float64 `json:"input_usd_per_million_tokens"`
	OutUSD1M *float64 `json:"output_usd_per_million_tokens"`
}

// Models fetches the models granted to key. The key travels only in the Authorization header.
func (c Client) Models(ctx context.Context, key string) ([]models.Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/v1/models", nil)
	if err != nil {
		return nil, &UnreachableError{BaseURL: c.BaseURL, Err: err}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	// The key must reach only the configured host, so a 3xx surfaces as an HTTPError instead of being followed.
	hc := *c.HTTP
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &UnreachableError{BaseURL: c.BaseURL, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, &HTTPError{Status: resp.StatusCode}
	}
	return decode(io.LimitReader(resp.Body, maxBody))
}

func decode(r io.Reader) ([]models.Model, error) {
	var body struct {
		Data *[]modelEntry `json:"data"`
	}
	if json.NewDecoder(r).Decode(&body) != nil || body.Data == nil {
		return nil, ErrUnexpected
	}
	if len(*body.Data) == 0 {
		return nil, ErrNoModels
	}
	out := make([]models.Model, 0, len(*body.Data))
	for _, e := range *body.Data {
		m := models.Model{Alias: e.ID, InUSD1M: e.InUSD1M, OutUSD1M: e.OutUSD1M}
		if e.Context != nil {
			ctx := int(math.Floor(*e.Context))
			m.Context = &ctx
		}
		out = append(out, m)
	}
	return out, nil
}
