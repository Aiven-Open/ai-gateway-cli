// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Each word must reach a real shell as one literal argument, whatever it holds.
func TestShellQuoteIsOneLiteralWordInEveryShell(t *testing.T) {
	// Percent signs must be quoted so cmd.exe does not expand %VAR%.
	if got := shellQuote("100%"); got != "'100%'" {
		t.Errorf("shellQuote(100%%) = %q, want '100%%'", got)
	}
	if got := shellQuote("%USERPROFILE%"); got != "'%USERPROFILE%'" {
		t.Errorf("shellQuote(%%USERPROFILE%%) = %q, want '%%USERPROFILE%%'", got)
	}
	if got := shellQuoteForOS("aiven/%PATH%", "windows"); got != "'aiven/%PATH%'" {
		t.Errorf("shellQuoteForOS(windows percent) = %q, want PowerShell single-quoted", got)
	}
	if got := shellQuoteForOS("aiven/$env:USERPROFILE", "windows"); got != "'aiven/$env:USERPROFILE'" {
		t.Errorf("shellQuoteForOS(windows dollar) = %q, want PowerShell single-quoted", got)
	}
	if got := shellQuote("aiven/claude-sonnet-5-5"); got != "aiven/claude-sonnet-5-5" {
		t.Errorf("a plain id is quoted: %q", got)
	}
	// Windows Claude settings path must resolve the literal userHome without %USERPROFILE% or Unix ~/.
	if got := claudeDirectCommand("windows", `C:\Users\Alice Smith`); strings.Contains(got, "~/") ||
		strings.Contains(got, "%USERPROFILE%") ||
		got != `claude --settings "C:\Users\Alice Smith\.claude\aiven.settings.json"` {
		t.Errorf("claudeDirectCommand(windows spaces) = %q", got)
	}
	if got := claudeDirectCommand(
		"windows",
		`C:\Users\alice`,
	); got != `claude --settings C:\Users\alice\.claude\aiven.settings.json` {
		t.Errorf("claudeDirectCommand(windows plain) = %q", got)
	}
	if got := claudeDirectCommand(
		"darwin",
		"/Users/alice",
	); got != "claude --settings ~/.claude/aiven.settings.json" {
		t.Errorf("claudeDirectCommand(darwin) = %q", got)
	}

	if runtime.GOOS == "windows" {
		t.Skip("POSIX/fish shell quoting execution test")
	}
	words := []string{
		"aiven/claude-sonnet-5-5",
		"",
		"two words",
		"it's",
		`model; touch "$HOME/shell-marker"; #`,
		"$(id)",
		"`id`",
		"~",
		"~/x",
		"*",
		"=ls",
		"a\\b",
		"!!",
		"100%",
		"%PATH%",
		"x\ny",
		"--flag=value",
		"名前",
	}
	for _, sh := range []string{"/bin/sh", "bash", "zsh", "fish"} {
		path, err := exec.LookPath(sh)
		if err != nil {
			t.Logf("%s not installed", sh)
			continue
		}
		t.Run(filepath.Base(sh), func(t *testing.T) {
			home := t.TempDir()
			for _, w := range words {
				cmd := exec.Command(path, "-c", `printf '%s\0' `+shellQuote(w))
				cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("%q: %v", w, err)
				}
				if got := strings.Split(
					strings.TrimSuffix(string(out), "\x00"),
					"\x00",
				); !slices.Equal(
					got,
					[]string{w},
				) {
					t.Errorf("%s: %q became %q", sh, w, got)
				}
			}
			if _, err := os.Stat(filepath.Join(home, "shell-marker")); err == nil {
				t.Error("a quoted word ran a statement")
			}
		})
	}
}
