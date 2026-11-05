// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/gateway"
)

const (
	keyPrompt   = "Paste your gateway key: "
	consoleHint = "Create a key in Aiven Console and run: aiven-ai-gateway login"
)

// login lists the key's models and saves it. It sends no inference request and configures no harness.
func login(ctx context.Context, env config.Env, deps Deps) int {
	p := newPresenter(env, deps, false)
	p.ui.line(0, "Gateway: "+safe(env.BaseURL))
	key, err := readKey(deps, keyPrompt)
	if err != nil {
		return fail(deps, err.Error())
	}
	table, err := fetchCatalogue(ctx, deps.gateway(env.BaseURL), key, p)
	if err != nil {
		return failGateway(deps, err)
	}
	p.begin("Saving the key")
	where, err := saveKey(env, deps, key)
	if err != nil {
		p.failed("the key was not saved")
		return fail(deps, err.Error())
	}
	p.passed("saved to " + where)
	p.ui.line(2, "Login checked model access only: no inference request was sent and no tool")
	p.ui.line(2, "was configured. Configure one before launching it:")
	p.ui.command(4, "aiven-ai-gateway setup <harness>")
	renderModels(newUI(deps.Stdout, deps.Term.StdoutTTY, deps.Term.StdoutWidth, plainRequested(env.Environ)), table)
	return 0
}

// listModels writes the granted catalogue to stdout, grouped by owner or publisher; it changes nothing.
func listModels(ctx context.Context, env config.Env, deps Deps) int {
	p := newPresenter(env, deps, false)
	p.ui.line(0, "Gateway: "+safe(env.BaseURL))
	_, table, code := grantedModels(ctx, "", env, deps, p)
	if code != 0 {
		return code
	}
	renderModels(newUI(deps.Stdout, deps.Term.StdoutTTY, deps.Term.StdoutWidth, plainRequested(env.Environ)), table)
	return 0
}

func logout(env config.Env, deps Deps) int {
	if err := removeKey(env, deps); err != nil {
		return fail(deps, err.Error())
	}
	_, _ = fmt.Fprintln(deps.Stderr, "Local key removed. Inherited environment credentials remain; no server key was revoked.")
	return 0
}

// readKey takes the key from the terminal prompt, else the first line of stdin, and checks its charset.
func readKey(deps Deps, prompt string) (string, error) {
	var key string
	var err error
	if deps.Prompter != nil {
		key, err = deps.Prompter.ReadSecret(prompt)
	} else {
		key, err = bufio.NewReader(deps.Stdin).ReadString('\n')
		if errors.Is(err, io.EOF) {
			err = nil
		}
	}
	if err != nil {
		return "", err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("empty key")
	}
	if !creds.ValidKey(key) {
		return "", errors.New("that does not look like a gateway key")
	}
	return key, nil
}

func failGateway(deps Deps, err error) int {
	if errors.Is(err, gateway.ErrUnauthorized) || errors.Is(err, gateway.ErrNoModels) {
		// A gateway message may end its own sentence.
		return fail(deps, strings.TrimSuffix(err.Error(), ".")+". "+consoleHint)
	}
	return fail(deps, err.Error())
}

func fail(deps Deps, msg string) int {
	// errors.Join separates its errors with newlines, which safe drops; keep the messages apart on one line.
	_, _ = fmt.Fprintf(deps.Stderr, "aiven-ai-gateway: %s\n", safe(strings.ReplaceAll(msg, "\n", "; ")))
	return 1
}
