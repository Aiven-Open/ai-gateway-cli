// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
)

// enterSelector answers every inline list as Enter does, through the list's own key handling, and refuses to read
// a secret.
type enterSelector struct{ secretCalls int }

func (s *enterSelector) Select(c choice) ([]int, error) {
	picked, finished, err := newSelectState(c).keys([]byte("\r"))
	if err == nil && !finished {
		err = errInputEnded
	}
	return picked, err
}

func (s *enterSelector) ReadSecret(string) (string, error) {
	s.secretCalls++
	return "", errInputEnded
}

func (s *enterSelector) ReadLine(string) (string, error) { return "", io.EOF }

type consentTransport func(*http.Request) (*http.Response, error)

func (f consentTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEnvironmentKeyConsentEnterRefuses(t *testing.T) {
	var sent []string
	sel := &enterSelector{}
	var stderr strings.Builder
	deps := inlineDeps(sel, &stderr)
	deps.HTTP = &http.Client{
		Transport: consentTransport(func(r *http.Request) (*http.Response, error) {
			sent = append(sent, r.Header.Get("Authorization"))
			return nil, errors.New("synthetic transport failure")
		}),
	}
	env := config.Env{
		BaseURL:  "https://gateway.example.invalid",
		UserHome: t.TempDir(),
		Environ:  []string{creds.EnvVar + "=sk-environment-key-0000"},
	}
	code := Run(context.Background(), []string{"setup", "omp"}, env, deps)
	if code != 1 || sel.secretCalls != 1 || len(sent) != 0 {
		t.Fatalf(
			"exit %d, hidden prompts %d, requests %q: %s",
			code,
			sel.secretCalls,
			sent,
			stderr.String(),
		)
	}
}
