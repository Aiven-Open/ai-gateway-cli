// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

func TestEarlyHelpPlainFlagBoundaryGrid(t *testing.T) {
	t.Parallel()
	var reference bytes.Buffer
	Informational(nil, nil, Deps{Stdout: &reference})
	for _, command := range []string{"login", "setup", "launch"} {
		for _, help := range []string{"-h", "--help"} {
			for _, args := range [][]string{
				{command, "--plain", help},
				{command, help, "--plain"},
				{command, "claude", "--plain", help},
				{command, help, "--plain", "--", "--plain"},
			} {
				for _, stdoutTTY := range []bool{false, true} {
					var out, errOut bytes.Buffer
					handled, code := Informational(args, []string{"TERM=xterm-256color"}, Deps{
						Stdout: &out, Stderr: &errOut, Term: Terminal{StdoutTTY: stdoutTTY},
					})
					if !handled || code != 0 || errOut.Len() != 0 || out.String() != reference.String() {
						t.Errorf("%q/TTY=%v: handled=%v code=%d out=%q err=%q", args, stdoutTTY, handled, code, out.String(), errOut.String())
					}
				}
			}
		}
	}
	for _, args := range [][]string{
		{"launch", "--help", "--", "--plain"},
		{"help", "--", "--plain"},
	} {
		var out bytes.Buffer
		handled, code := Informational(args, nil, Deps{Stdout: &out, Term: Terminal{StdoutTTY: true}})
		if !handled || code != 0 || !strings.Contains(out.String(), "\x1b[") {
			t.Fatalf("native-side --plain changed launcher help: %q: %q", args, out.String())
		}
	}
	var out bytes.Buffer
	handled, code := Informational([]string{"launch", "claude", "--", "--plain", "--help"}, nil, Deps{Stdout: &out})
	if handled || code != 0 || out.Len() != 0 {
		t.Fatalf("native help intercepted: handled=%v code=%d out=%q", handled, code, out.String())
	}
}

// The final renderer must either preserve a shell word exactly or omit its executable command.
func TestCommandRenderingEquivalenceGrid(t *testing.T) {
	t.Parallel()
	style := regexp.MustCompile("\x1b\\[[0-9;]*m")
	words := []string{"", "two words", "名前", "café", "👩\u200d💻", "it's", "$(id)", "`id`", `model; touch "$HOME/shell-marker"; #`}
	for r := rune(0x20); r < 0x7f; r++ {
		words = append(words, "before"+string(r)+"after")
	}
	var controls []string
	for r := rune(0); r < 0x20; r++ {
		controls = append(controls, string(r))
	}
	for r := rune(0x7f); r <= 0x9f; r++ {
		controls = append(controls, string(r))
	}
	controls = append(controls, "\u061c", "\u200e", "\u200f", "\u202a", "\u202e", "\u2066", "\u2069", "\xff")
	for _, tool := range []string{"pi", "opencode", "omp"} {
		for _, control := range controls {
			for _, id := range []string{control + "model", "mo" + control + "del", "model" + control} {
				for _, colored := range []bool{false, true} {
					var out bytes.Buffer
					u := newUI(&out, colored, nil, !colored)
					u.command(0, directCommand(tool, []models.Model{{Alias: id}}, false))
					if out.Len() != 0 {
						t.Fatalf("%s/color=%v/control=%q advertised altered command %q", tool, colored, id, out.String())
					}
				}
			}
		}
		if runtime.GOOS == "windows" {
			continue
		}
		for _, sh := range []string{"/bin/sh", "bash", "zsh"} {
			shell, err := exec.LookPath(sh)
			if err != nil {
				t.Logf("%s unavailable; its execution grid was not run", sh)
				continue
			}
			t.Run(tool+"/"+filepath.Base(sh), func(t *testing.T) {
				home := t.TempDir()
				for _, id := range words {
					for _, colored := range []bool{false, true} {
						var out bytes.Buffer
						u := newUI(&out, colored, nil, !colored)
						u.command(0, directCommand(tool, []models.Model{{Alias: id}}, false))
						printed := style.ReplaceAllString(out.String(), "")
						if printed == "" {
							t.Fatalf("printable token %q had no command", id)
						}
						cmd := exec.Command(shell, "-c", tool+`() { printf '%s\0' "$@"; }; `+printed)
						cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
						got, err := cmd.Output()
						if err != nil {
							t.Fatalf("%s: %q: %v", sh, printed, err)
						}
						flag := "--model"
						if tool == "opencode" {
							flag = "-m"
						}
						want := fmt.Sprintf("%s\x00aiven/%s\x00", flag, id)
						if string(got) != want {
							t.Fatalf("%s: %q passed %q, want %q", sh, printed, got, want)
						}
					}
				}
				if _, err := os.Stat(filepath.Join(home, "shell-marker")); !os.IsNotExist(err) {
					t.Fatal("a rendered command executed shell syntax")
				}
			})
		}
	}
}
