// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestListedSelectorsKeepControlsInConfigAndNativeArguments(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"pi", "opencode"} {
		for _, id := range []string{"claude-model\nname", "claude-model\rname", "claude-model\tname", "claude-model\x1b[31m", "claude-model\u202ename"} {
			t.Run(tool+"/"+id, func(t *testing.T) {
				t.Parallel()
				g := newGatewayDouble(t)
				body, err := json.Marshal(map[string]any{"data": []map[string]string{
					{"id": id, "owned_by": "Aiven"}, {"id": "claude-sonnet"},
				}})
				if err != nil {
					t.Fatal(err)
				}
				g.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						_, _ = w.Write(body)
					} else {
						_, _ = w.Write([]byte(`{"choices":[],"content":[]}`))
					}
				})
				s := newSession(t, g).loggedIn(keyValid)
				r := s.run("setup", tool, "--yes")
				if r.code != 0 || strings.Contains(r.stderr, "\x1b") || strings.Contains(r.stderr, "\u202e") {
					t.Fatalf("unsafe displayed selector: code=%d stderr=%q", r.code, r.stderr)
				}
				if tool == "pi" {
					if got := piModels(t, s); len(got) != 2 || got[0]["id"] != id {
						t.Fatalf("stored Pi catalogue altered the selector: %v", got)
					}
				} else {
					models := opencodeConfig(t, s)["provider"].(map[string]any)["aiven"].(map[string]any)["models"].(map[string]any)
					if len(models) != 2 || models[id] == nil {
						t.Fatalf("stored OpenCode catalogue altered the selector: %v", models)
					}
				}
				flag := "--model"
				if tool == "opencode" {
					flag = "-m"
				}
				if r := s.run("launch", tool, "--", flag, "aiven/"+id); r.code != 0 {
					t.Fatal(r.stderr)
				}
				if got := s.launched().argv; !slices.Equal(got, []string{tool, flag, "aiven/" + id}) {
					t.Fatalf("launch passed %q instead of the exact selector", got)
				}
			})
		}
	}
}

// Public selectors remain copyable; the renderer's separate shell grid covers hostile words.
func TestCopiedNativeCommandPassesThePublicSelectorLiterally(t *testing.T) {
	for _, tool := range []string{"pi", "opencode"} {
		t.Run(tool, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
			r := s.run("setup", tool, "--yes")
			if r.code != 0 {
				t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
			}
			var direct string
			for _, l := range strings.Split(r.stderr, "\n") {
				if l = strings.TrimSpace(l); strings.HasPrefix(l, tool+" -") {
					direct = l
				}
			}
			if direct == "" {
				t.Fatalf("no direct command in %q", r.stderr)
			}
			if runtime.GOOS == "windows" {
				return
			}
			// The tool is a shell function that prints its arguments, one per line.
			home := t.TempDir()
			cmd := exec.Command("/bin/sh", "-c", tool+`() { printf '%s\n' "$@"; }; `+direct)
			cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("%q: %v", direct, err)
			}
			flag := map[string]string{"pi": "--model", "opencode": "-m"}[tool]
			if got, want := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"), []string{flag, "aiven/claude-sonnet"}; !slices.Equal(got, want) {
				t.Errorf("%q passed %q, want %q", direct, got, want)
			}
			if _, err := os.Stat(filepath.Join(home, "shell-marker")); err == nil {
				t.Errorf("%q ran a statement", direct)
			}
		})
	}
}
