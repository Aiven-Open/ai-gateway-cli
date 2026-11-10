// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// tableHarnesses are the verbs whose launch needs only the key and the model table.
var tableHarnesses = []string{"codex", "pi", "opencode"}

func TestTableHarnessesFailBeforeGatewayOrWritesWhenBinaryMissing(t *testing.T) {
	for _, h := range tableHarnesses {
		t.Run(h, func(t *testing.T) {
			g := newGatewayDouble(t)
			s := newSession(t, g).loggedIn(keyValid)
			s.runner.missing = []string{h}
			r := s.run("launch", h)
			if want := "aiven-ai-gateway: " + h + " is required but not installed; install it, then run aiven-ai-gateway setup " + h + "\n"; r.code != 1 || r.stdout != "" || r.stderr != want {
				t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
			if n := len(g.journal()); n != 0 {
				t.Errorf("gateway got %d requests", n)
			}
			if files := s.homeFiles(); len(files) != 0 {
				t.Errorf("wrote %v", files)
			}
			if len(s.runner.calls) != 0 {
				t.Errorf("exec'd %v", s.runner.calls)
			}
		})
	}
}

func TestTableHarnessesExecNothingWhenLaunchCannotProceed(t *testing.T) {
	cases := []struct {
		name, key, want string
	}{
		{"no key", "", "aiven-ai-gateway: no key stored. Run: aiven-ai-gateway login\n"},
		{"gateway rejects key", "sk-bad", catalogueFailed("the gateway rejected the key", "key rejected by gateway (401): Authentication Error. Create a key in Aiven Console and run: aiven-ai-gateway login")},
		{"key grants no models", keyEmpty, catalogueFailed("the key grants no models", "key grants no models. Create a key in Aiven Console and run: aiven-ai-gateway login")},
	}
	for _, h := range tableHarnesses {
		for _, tc := range cases {
			t.Run(h+"/"+tc.name, func(t *testing.T) {
				s := newSession(t, newGatewayDouble(t))
				s.loggedIn(keyValid)
				if r := s.run("setup", h, "--yes"); r.code != 0 {
					t.Fatal(r.stderr)
				}
				s.run("logout")
				if tc.key != "" {
					s.loggedIn(tc.key)
				}
				before := configurationSnapshot(t, s.fakeHome, s.env.WorkDir)
				r := s.run("launch", h)
				want := tc.want
				if h == "opencode" && tc.key == "" {
					want = "aiven-ai-gateway: OpenCode requires a key saved for this gateway. Run: aiven-ai-gateway login\n"
				}
				want = "Gateway: " + s.env.BaseURL + "\n" + want
				if r.code != 1 || r.stdout != "" || r.stderr != want {
					t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
				}
				if len(s.runner.calls) != 0 {
					t.Errorf("exec'd %v", s.runner.calls)
				}
				if after := configurationSnapshot(t, s.fakeHome, s.env.WorkDir); !reflect.DeepEqual(before, after) {
					t.Error("launch changed files")
				}
			})
		}
	}
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte(text), 0o644)
}

var userConfigs = map[string]struct{ path, key string }{
	"pi": {".pi/agent/models.json", "providers"}, "opencode": {".config/opencode/opencode.json", "provider"},
}

func TestUserConfigMergeKeepsOtherProvidersAndRefusesMalformed(t *testing.T) {
	for h, uc := range userConfigs {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
		path := filepath.Join(s.fakeHome, uc.path)
		before := "\xef\xbb\xbf// comment\n{\"keep\":{\"big\":12345678901234567890,},/*block*/\"" + uc.key + "\":{\"other\":{\"apiKey\":\"$OTHER\",},},}"
		writeFile(t, path, before)
		for range 2 {
			if r := s.run("setup", h, "--yes"); r.code != 0 {
				t.Fatalf("%s: exit %d, stderr %q", h, r.code, r.stderr)
			}
		}
		data, _ := os.ReadFile(path)
		bak := latestBackup(t, path)
		providers := readJSON(t, path)[uc.key].(map[string]any)
		kept := "\xef\xbb\xbf// comment\n{\"keep\":{\"big\":12345678901234567890,},/*block*/\"" + uc.key + "\":{\"other\":{\"apiKey\":\"$OTHER\",},"
		if h == "pi" {
			// Pi reads // comments and trailing commas in models.json, but not block comments.
			kept = strings.Replace(kept, "/*block*/", "", 1)
		}
		if bak != before || !strings.HasPrefix(string(data), kept) || providers["other"] == nil || providers["aiven"] == nil {
			t.Errorf("%s merged = %s, bak = %s", h, data, bak)
		}
		for _, bad := range []string{`{"broken`, `null`, `{}]`, `{"` + uc.key + `":[]}`, `{"keep":1/*x*/2}`, `{}/*unfinished`, `{"keep":1}/*`} {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
			p := filepath.Join(s.fakeHome, uc.path)
			writeFile(t, p, bad)
			if r := s.run("setup", h, "--yes"); r.code != 1 || !strings.Contains(r.stderr, "not a JSON object") || len(s.runner.calls) != 0 {
				t.Fatalf("%s/%s: exit %d, stderr %q", h, bad, r.code, r.stderr)
			}
		}
	}
}

// A tool runs its key helper again on its own schedule, Codex after a 401 or its refresh interval and Claude Code
// after five minutes, so a login to another gateway after setup must not change the key it sends: each tool's
// helper reads the key file of the gateway it was set up for, which that login leaves alone.
func TestEachToolsKeyHelperKeepsItsGatewaysKeyAfterALoginElsewhere(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs the POSIX helpers through sh")
	}
	for _, tool := range []string{"claude", "codex", "pi", "omp", "opencode"} {
		t.Run(tool, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
			if r := s.run("setup", tool, "--key-stdin", "--yes"); r.code != 0 {
				t.Fatalf("setup: exit %d, stderr %q", r.code, r.stderr)
			}
			read := keyReader(t, s, tool)
			if got := read(); got != keyValid+"\n" {
				t.Fatalf("helper after setup printed %q", got)
			}
			s.env.BaseURL = newGatewayDouble(t).URL
			if r := s.stdin(keyFlash).run("login"); r.code != 0 {
				t.Fatalf("login elsewhere: exit %d, stderr %q", r.code, r.stderr)
			}
			if got := read(); got != keyValid+"\n" {
				t.Errorf("helper after a login elsewhere printed %q, want the key of its own gateway", got)
			}
		})
	}
}

// keyReader returns what tool, as setup configured it, would send as its key: its helper's output, run as the tool
// runs it with the session's home, or for OpenCode the file its {file:} reference names.
func keyReader(t *testing.T, s *session, tool string) func() string {
	t.Helper()
	var name string
	var args []string
	switch tool {
	case "codex":
		auth := codexConfigOf(t, s).ModelProviders["aiven"].Auth
		name, args = auth.Command, auth.Args
	case "opencode":
		ref, _ := opencodeConfig(t, s)["provider"].(map[string]any)["aiven"].(map[string]any)["options"].(map[string]any)["apiKey"].(string)
		rel, ok := strings.CutPrefix(strings.TrimSuffix(ref, "}"), "{file:~/")
		if !ok {
			t.Fatalf("opencode apiKey = %q", ref)
		}
		return func() string { return readOrEmpty(t, filepath.Join(s.fakeHome, rel)) }
	default:
		var line string
		switch tool {
		case "claude":
			line, _ = readJSON(t, filepath.Join(s.fakeHome, ".claude", "aiven.settings.json"))["apiKeyHelper"].(string)
		case "pi":
			line, _ = readJSON(t, piFile(s, "models.json"))["providers"].(map[string]any)["aiven"].(map[string]any)["apiKey"].(string)
			line = strings.TrimPrefix(line, "!")
		case "omp":
			line, _ = readYAML(t, filepath.Join(s.fakeHome, ".omp/agent/models.yml"))["providers"].(map[string]any)["aiven"].(map[string]any)["apiKey"].(string)
			line = strings.TrimPrefix(line, "!")
		}
		name, args = "/bin/sh", []string{"-c", line}
	}
	return func() string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + s.fakeHome}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s helper %q %q: %v", tool, name, args, err)
		}
		return string(out)
	}
}
