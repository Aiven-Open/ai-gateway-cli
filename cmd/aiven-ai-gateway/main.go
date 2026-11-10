// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Command aiven-ai-gateway launches coding harnesses against the Aiven AI Gateway.
package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/aiven-open/ai-gateway-cli/internal/cli"
	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/execx"
)

// Set by GoReleaser ldflags (.goreleaser.yaml).
var version, commit, date = "dev", "none", "unknown"

func main() {
	info, _ := debug.ReadBuildInfo()
	b := build(info)
	// Answered before config loads so a broken environment still reports the build.
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		os.Exit(cli.Run(context.Background(), os.Args[1:], config.Env{}, cli.Deps{Stdout: os.Stdout, Build: b}))
	}
	deps := cli.Deps{
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Runner:   execx.Syscall{},
		Prompter: cli.TerminalPrompter(os.Stdin, os.Stderr),
		Term:     cli.DetectTerminal(os.Stdin, os.Stdout, os.Stderr),
		Build:    b,
	}
	if handled, code := cli.Informational(os.Args[1:], os.Environ(), deps); handled {
		os.Exit(code)
	}
	env, err := config.Load(os.Environ())
	if err != nil {
		fmt.Fprintln(os.Stderr, "aiven-ai-gateway:", err)
		os.Exit(1)
	}
	if env.UserHome == "" {
		fmt.Fprintln(os.Stderr, "aiven-ai-gateway:", config.ErrNoHome)
		os.Exit(1)
	}
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "aiven-ai-gateway: cannot read working directory:", err)
		os.Exit(1)
	}
	env.WorkDir = wd
	os.Exit(cli.Run(context.Background(), os.Args[1:], env, deps))
}

// build is the ldflags metadata; a `go install` build has none, so it falls back to the module version and VCS stamp.
func build(info *debug.BuildInfo) cli.Build {
	b := cli.Build{Version: version, Commit: commit, Date: date}
	if version != "dev" || info == nil {
		return b
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		b.Version = v
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			b.Commit = s.Value
		case "vcs.time":
			b.Date = s.Value
		}
	}
	return b
}
