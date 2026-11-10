// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package gateway reads the key's model table from AI Gateway `GET /v1/models` and checks the key can complete.
package gateway

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aiven-open/ai-gateway-cli/internal/models"
	"github.com/aiven-open/ai-gateway-cli/internal/redact"
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

// HTTPError retains bounded, credential-redacted gateway error details.
type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string {
	text := fmt.Sprintf("gateway returned HTTP %d", e.Status)
	if e.Status == http.StatusUnauthorized {
		text = ErrUnauthorized.Error()
	}
	if e.Message != "" {
		text += ": " + e.Message
	}
	return text
}

// Unwrap classifies only HTTP 401 as rejected authentication.
func (e *HTTPError) Unwrap() error {
	if e.Status == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	return nil
}

// NewHTTPClient returns the client the launcher uses unless a caller injects one.
func NewHTTPClient() *http.Client { return &http.Client{Timeout: Timeout} }

// Client talks to one gateway.
type Client struct {
	HTTP    *http.Client
	BaseURL string
}

type modelEntry struct {
	ID string `json:"id"`
	// OwnedBy is the OpenAI-shaped owner; the gateway fills it from the model's creator.
	OwnedBy     string   `json:"owned_by"`
	DisplayName string   `json:"display_name"`
	Context     *float64 `json:"context_window_tokens"`
	MaxInput    *float64 `json:"max_input_tokens"`
	MaxOut      *float64 `json:"max_output_tokens"`
	InUSD1M     *float64 `json:"input_usd_per_million_tokens"`
	OutUSD1M    *float64 `json:"output_usd_per_million_tokens"`
}

// Models fetches every model the gateway lists for key.
// The key travels only in the Authorization header.
func (c Client) Models(ctx context.Context, key string) ([]models.Model, error) {
	resp, err := c.do(ctx, http.MethodGet, "/v1/models", key, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return decode(io.LimitReader(resp.Body, maxBody))
}

// Probe sends one minimal request on protocol. It does not test native client requests.
func (c Client) Probe(ctx context.Context, key, model string, protocol models.Protocol) (int, error) {
	payload := map[string]any{"model": model}
	path, field := "", ""
	switch protocol {
	case models.ChatCompletions, models.Messages:
		payload["messages"] = []map[string]string{{"role": "user", "content": "hi"}}
		payload["max_tokens"] = 1
		path, field = "/v1/chat/completions", "choices"
		if protocol == models.Messages {
			path, field = "/v1/messages", "content"
		}
	case models.Responses:
		payload["input"], payload["max_output_tokens"] = "hi", 1
		path, field = "/v1/responses", "output"
	default:
		return 0, fmt.Errorf("unresolved inference protocol")
	}
	body, _ := json.Marshal(payload)
	resp, err := c.do(ctx, http.MethodPost, path, key, bytes.NewReader(body))
	if err != nil {
		var he *HTTPError
		if errors.As(err, &he) {
			return he.Status, err
		}
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("read basic check response: %w", err)
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil {
		return resp.StatusCode, ErrUnexpected
	}
	if value, exists := envelope["error"]; exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return resp.StatusCode, &HTTPError{Status: resp.StatusCode, Message: errorMessage(data, key)}
	}
	var result []json.RawMessage
	if value := envelope[field]; len(value) == 0 || string(value) == "null" || json.Unmarshal(value, &result) != nil {
		return resp.StatusCode, ErrUnexpected
	}
	return resp.StatusCode, nil
}

// do sends one request with key as Bearer and maps 401, other non-2xx and transport failures to the package errors.
func (c Client) do(ctx context.Context, method, path, key string, body io.Reader) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	// A returned body is read by the caller; keep cancellation tied to its close.
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		cancel()
		return nil, &UnreachableError{BaseURL: c.BaseURL, Err: err}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if path == "/v1/messages" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	// The key must reach only the configured host, so a 3xx surfaces as an HTTPError instead of being followed.
	hc := *c.HTTP
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		cancel()
		return nil, &UnreachableError{BaseURL: c.BaseURL, Err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		cancel()
		return nil, &HTTPError{Status: resp.StatusCode, Message: errorMessage(data, key)}
	}
	resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}

// credentialText matches what may be a credential in a gateway message: a bearer token, a value shaped like a known
// credential, or the value of a field named like one, quoted or bare. The name and the word bearer stay.
var credentialText = regexp.MustCompile(`(?i)(\bbearer\s+)\S+|` + redact.Token + `|(["']?` + redact.Name +
	`["']?\s*[:=]\s*)(?:"(?:[^"\\]|\\.)*"|'[^']*'|(?:bearer\s+)?[^\s,;}]+)`)

func errorMessage(data []byte, key string) string {
	var body struct {
		Message string          `json:"message"`
		Code    json.RawMessage `json:"code"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &body) != nil {
		return ""
	}
	if len(body.Error) != 0 && !bytes.Equal(bytes.TrimSpace(body.Error), []byte("null")) {
		var message string
		var nested struct {
			Message string          `json:"message"`
			Code    json.RawMessage `json:"code"`
		}
		if json.Unmarshal(body.Error, &message) == nil {
			body.Message = message
		} else if json.Unmarshal(body.Error, &nested) == nil {
			body.Message, body.Code = nested.Message, nested.Code
		}
	}
	text := body.Message
	var code string
	if json.Unmarshal(body.Code, &code) == nil && code != "" {
		text = code + ": " + text
	}
	// Control characters go before redaction: one inside a credential would otherwise hide its tail.
	text = label(text)
	if key != "" {
		text = strings.ReplaceAll(text, key, "[redacted]")
	}
	text = credentialText.ReplaceAllStringFunc(text, func(m string) string {
		sub := credentialText.FindStringSubmatch(m)
		return sub[1] + sub[2] + "[redacted]"
	})
	runes := []rune(text)
	if len(runes) > 512 {
		text = string(runes[:512]) + "…"
	}
	return text
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
		out = append(out, models.Model{
			Alias: e.ID, Owner: label(e.OwnedBy), Name: label(e.DisplayName),
			InUSD1M: e.InUSD1M, OutUSD1M: e.OutUSD1M, MaxOutput: floor(e.MaxOut),
			// A listing may carry only `max_input_tokens`.
			Context: cmp.Or(floor(e.Context), floor(e.MaxInput)),
		})
	}
	return out, nil
}

// label keeps catalogue text meant only for display, dropping control characters so a hostile entry cannot move the
// cursor or recolour the terminal. Ids are not passed through it: configuration and argv need them exactly.
func label(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError || unicode.Is(unicode.Bidi_Control, r) {
			return -1
		}
		return r
	}, s))
}

func floor(f *float64) *int {
	if f == nil {
		return nil
	}
	n := int(math.Floor(*f))
	if n <= 0 {
		return nil
	}
	return &n
}
