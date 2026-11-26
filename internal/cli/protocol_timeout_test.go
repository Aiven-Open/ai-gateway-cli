// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/gateway"
)

// A check that times out is sent once more; a second timeout is reported as one, with the limit.
func TestSetupRetriesTimedOutCheckOnceAndReportsTimeout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		slow int32
		code int
	}{
		{"second try answers", 1, 0},
		{"both tries time out", 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := newGatewayDouble(t)
			var posts atomic.Int32
			base := g.Config.Handler
			g.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && posts.Add(1) <= tc.slow {
					select {
					case <-r.Context().Done():
					case <-time.After(5 * time.Second):
					}
					return
				}
				base.ServeHTTP(w, r)
			})
			s := newSession(t, g).stdin(keyValid)
			client := *g.Client()
			client.Timeout = 300 * time.Millisecond
			s.deps.HTTP = &client
			r := s.run("setup", "--key-stdin", "--yes", "claude")
			if r.code != tc.code || posts.Load() != 2 {
				t.Fatalf("exit %d after %d checks, stderr %q", r.code, posts.Load(), r.stderr)
			}
			if tc.code != 0 && (!strings.Contains(r.stderr, "did not answer within 300ms") ||
				strings.Contains(r.stderr, "cannot reach gateway")) {
				t.Errorf("timeout reported as %q", r.stderr)
			}
		})
	}
}

// stallTransport answers every POST with headers and a body whose read blocks until the test releases it, then fails as
// a timeout; every other request goes to next. No clock is involved.
type stallTransport struct {
	next    http.RoundTripper
	posts   atomic.Int32
	reading chan struct{}
	release chan struct{}
	once    sync.Once
}

type stalledBody struct{ s *stallTransport }

type timeoutError struct{}

// A check whose body stalls after the headers is reported as a timeout and not sent again, since the gateway took it.
func TestSetupReportsBodyTimeoutWithoutSendingTheCheckAgain(t *testing.T) {
	t.Parallel()
	g := newGatewayDouble(t)
	client := *g.Client()
	client.Timeout = 0
	stall := &stallTransport{
		next: client.Transport, reading: make(chan struct{}), release: make(chan struct{}),
	}
	client.Transport = stall
	go func() {
		<-stall.reading
		close(stall.release)
	}()
	t.Cleanup(func() { stall.once.Do(func() { close(stall.reading) }) })
	s := newSession(t, g).stdin(keyValid)
	s.deps.HTTP = &client
	r := s.run("setup", "--key-stdin", "--yes", "claude")
	if r.code != 1 || stall.posts.Load() != 1 {
		t.Fatalf("exit %d after %d checks, stderr %q", r.code, stall.posts.Load(), r.stderr)
	}
	if !strings.Contains(r.stderr, "did not answer within "+gateway.Timeout.String()) ||
		strings.Contains(r.stderr, "read basic check") {
		t.Errorf("timeout reported as %q", r.stderr)
	}
}

func (s *stallTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodPost {
		return s.next.RoundTrip(r)
	}
	s.posts.Add(1)
	return &http.Response{
		StatusCode: http.StatusOK, Status: "200 OK", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{}, Body: stalledBody{s}, Request: r,
	}, nil
}

func (b stalledBody) Read([]byte) (int, error) {
	b.s.once.Do(func() { close(b.s.reading) })
	<-b.s.release
	return 0, timeoutError{}
}

func (stalledBody) Close() error { return nil }

func (timeoutError) Error() string   { return "stalled body" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
