// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"io"
	"net/http"
	"time"

	"github.com/Aiven-Open/ai-gateway-cli/internal/execx"
	"github.com/Aiven-Open/ai-gateway-cli/internal/gateway"
	"github.com/Aiven-Open/ai-gateway-cli/internal/keystore"
)

// Deps are the process resources commands use; tests substitute them.
type Deps struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// HTTP defaults to a client with the gateway timeout.
	HTTP     *http.Client
	Keystore keystore.Store
	// Runner execs the harness a launch command prepared.
	Runner execx.Runner
	// Prompter reads a key silently from a terminal; nil means read one line from Stdin.
	Prompter Prompter
	// Now defaults to time.Now.
	Now func() time.Time
}

// Prompter talks to an interactive terminal; nil in Deps means stdin is not one.
type Prompter interface {
	ReadSecret(prompt string) (string, error)
	// ReadLine shows prompt and returns the answer without its newline; io.EOF when input ended.
	ReadLine(prompt string) (string, error)
}

func (d Deps) gateway(baseURL string) gateway.Client {
	c := d.HTTP
	if c == nil {
		c = gateway.NewHTTPClient()
	}
	return gateway.Client{HTTP: c, BaseURL: baseURL}
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}
