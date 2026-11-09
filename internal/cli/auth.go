// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/Aiven-Open/ai-gateway-cli/internal/config"
	"github.com/Aiven-Open/ai-gateway-cli/internal/gateway"
	"github.com/Aiven-Open/ai-gateway-cli/internal/keystore"
	"github.com/Aiven-Open/ai-gateway-cli/internal/models"
)

const (
	keyPrompt   = "Paste your gateway key: "
	consoleHint = "Create a key in Aiven Console and run: aiven-llm login"
)

var keyCharset = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func login(ctx context.Context, env config.Env, deps Deps) int {
	key, err := readKey(deps)
	if err != nil {
		return fail(deps, err.Error())
	}
	table, err := deps.gateway(env.BaseURL).Models(ctx, key)
	if err != nil {
		return failGateway(deps, err)
	}
	where, err := deps.Keystore.Set(keystore.Record{BaseURL: env.BaseURL, Key: key, WrittenAt: deps.now()})
	if err != nil {
		return fail(deps, err.Error())
	}
	fmt.Fprintf(deps.Stdout, "Key stored in %s. Models you can use:\n%s", where, models.Table(table))
	return 0
}

func listModels(ctx context.Context, env config.Env, deps Deps) int {
	_, table, code := grantedModels(ctx, "", env, deps)
	if code != 0 {
		return code
	}
	fmt.Fprint(deps.Stdout, models.Table(table))
	return 0
}

func logout(env config.Env, deps Deps) int {
	if err := deps.Keystore.Delete(env.BaseURL); err != nil {
		return fail(deps, err.Error())
	}
	fmt.Fprintln(deps.Stdout, "key removed")
	return 0
}

// readKey takes the key from the terminal prompt, else the first line of stdin, and checks its charset.
func readKey(deps Deps) (string, error) {
	var key string
	var err error
	if deps.Prompter != nil {
		key, err = deps.Prompter.ReadSecret(keyPrompt)
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
	if !keyCharset.MatchString(key) {
		return "", errors.New("that does not look like a gateway key")
	}
	return key, nil
}

func failGateway(deps Deps, err error) int {
	if errors.Is(err, gateway.ErrUnauthorized) || errors.Is(err, gateway.ErrNoModels) {
		return fail(deps, err.Error()+". "+consoleHint)
	}
	return fail(deps, err.Error())
}

func fail(deps Deps, msg string) int {
	fmt.Fprintf(deps.Stderr, "aiven-llm: %s\n", msg)
	return 1
}
