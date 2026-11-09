// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Command aiven-llm launches coding harnesses against the Aiven AI Gateway.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Aiven-Open/ai-gateway-cli/internal/cli"
	"github.com/Aiven-Open/ai-gateway-cli/internal/config"
	"github.com/Aiven-Open/ai-gateway-cli/internal/execx"
	"github.com/Aiven-Open/ai-gateway-cli/internal/keystore"
)

func main() {
	env, err := config.Load(os.Environ())
	if err != nil {
		fmt.Fprintln(os.Stderr, "aiven-llm:", err)
		os.Exit(1)
	}
	if env.Home == "" {
		fmt.Fprintln(os.Stderr, "aiven-llm: cannot locate a home directory. Set AIVEN_LLM_HOME")
		os.Exit(1)
	}
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "aiven-llm: cannot read working directory:", err)
		os.Exit(1)
	}
	env.WorkDir = wd
	store, err := keystore.New(env.Keystore, env.Home, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "aiven-llm:", err)
		os.Exit(1)
	}
	deps := cli.Deps{
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Keystore: store,
		Runner:   execx.Syscall{},
		Prompter: cli.TerminalPrompter(os.Stdin, os.Stderr),
	}
	os.Exit(cli.Run(context.Background(), os.Args[1:], env, deps))
}
