// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/gateway"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

// Anthropic picks have no count limit, so the goroutines that wait for a probe slot must not grow with them.
func TestProbeWorkersStayBounded(t *testing.T) {
	const picks = 200
	release, full := make(chan struct{}), make(chan struct{}, 1)
	var inFlight atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inFlight.Add(1) == maxConcurrentProbes {
			full <- struct{}{}
		}
		<-release
		_, _ = w.Write([]byte(`{"id":"synthetic","type":"message","content":[]}`))
	}))
	defer srv.Close()
	plan := protocolPlan{deps: map[string][]probeKey{}}
	for i := range picks {
		pk := probeKey{protocol: models.Messages, model: fmt.Sprintf("model-%03d", i)}
		plan.checks = append(plan.checks, protocolCheck{key: pk})
	}
	run := &setupRun{p: newPresenter(config.Env{}, Deps{Stderr: io.Discard}, true)}
	gw := gateway.Client{HTTP: srv.Client(), BaseURL: srv.URL}
	before := runtime.NumGoroutine()
	done := make(chan struct{})
	go func() {
		defer close(done)
		checkProtocols(context.Background(), gw, "sk-test-pool", nil, plan, run, nil)
	}()
	<-full
	grown := runtime.NumGoroutine() - before
	close(release)
	<-done
	if grown >= picks/2 {
		t.Errorf("%d more goroutines while %d checks waited", grown, picks)
	}
	for _, c := range run.checks {
		if c.err != nil {
			t.Fatalf("%s: %v", c.key.model, c.err)
		}
	}
}
