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
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell quoting test")
	}
	words := []string{
		"aiven/claude-sonnet-5-5", "", "two words", "it's", `model; touch "$HOME/shell-marker"; #`, "$(id)", "`id`",
		"~", "~/x", "*", "=ls", "a\\b", "!!", "x\ny", "--flag=value", "名前",
	}
	for _, sh := range []string{"/bin/sh", "bash", "zsh"} {
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
				if got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00"); !slices.Equal(got, []string{w}) {
					t.Errorf("%s: %q became %q", sh, w, got)
				}
			}
			if _, err := os.Stat(filepath.Join(home, "shell-marker")); err == nil {
				t.Error("a quoted word ran a statement")
			}
		})
	}
	if got := shellQuote("aiven/claude-sonnet-5-5"); got != "aiven/claude-sonnet-5-5" {
		t.Errorf("a plain id is quoted: %q", got)
	}
}
