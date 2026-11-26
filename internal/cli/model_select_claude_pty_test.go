// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build darwin || linux

package cli

import (
	"bufio"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
)

func TestPTYChooseClaudeModelsTickDefaultAndApply(t *testing.T) {
	srv, _ := inlineGateway(t, claudeMixCatalogue)
	env, home := inlineSetupEnv(t, srv.URL)
	p := openPTY(t)
	deps := Deps{
		Stdin:    strings.NewReader("synthetic-key\n"),
		Stdout:   io.Discard,
		Stderr:   p.tty,
		HTTP:     srv.Client(),
		Runner:   allInstalled{},
		Prompter: terminalPrompter{in: p.tty, stderr: p.tty, lines: bufio.NewReader(p.tty)},
		Term:     Terminal{StdinTTY: true, StderrTTY: true, StderrWidth: func() int { return 100 }},
	}
	done := make(chan int, 1)
	go func() {
		done <- Run(t.Context(), []string{"setup", "--key-stdin", "claude"}, env, deps)
	}()
	for _, step := range []struct{ wait, keys string }{
		{"Models for Claude Code", "\x1b[B\r"},                                  // Choose models
		{"Choose models for Claude Code", "\x1b[B\x1b[B\x1b[B\x1b[B \x1b[B \r"}, // gemini, then gpt
		{"Default model for Claude Code", "\r"},                                 // gemini-4-neon
		{"Use Aiven AI Gateway as the default for Claude Code?", "\x1b[B\r"},    // No
		{"Apply these changes to Claude Code?", "\r"},                           // Apply
	} {
		p.waitFor(t, step.wait)
		p.typeKeys(t, step.keys)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("setup exit %d; terminal showed %q", code, p.output())
		}
	case <-time.After(10 * time.Second):
		p.typeKeys(t, "\x03")
		t.Fatalf("setup did not finish; terminal showed %q", p.output())
	}
	path := filepath.Join(home, claude.SettingsFile)
	slotEnv := readJSONKey(t, path, "env").(map[string]any)
	if readJSONKey(t, path, "model") != "gemini-4-neon" ||
		slotEnv["ANTHROPIC_CUSTOM_MODEL_OPTION"] != "gemini-4-neon" ||
		slotEnv["ANTHROPIC_DEFAULT_FABLE_MODEL"] != "gpt-6-astra" {
		t.Errorf("settings env = %v", slotEnv)
	}
}
