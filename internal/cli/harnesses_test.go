// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"os"
	"path/filepath"
	"slices"
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
			r := s.run(h)
			if want := "aiven-llm: " + h + " is required but not installed\n"; r.code != 1 || r.stdout != "" || r.stderr != want {
				t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
			if n := len(g.journal()); n != 0 {
				t.Errorf("gateway got %d requests", n)
			}
			for _, f := range s.homeFiles() {
				if filepath.Dir(f) != "aig-keys" {
					t.Errorf("wrote %s", f)
				}
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
		{"no key", "", "aiven-llm: no key stored. Run: aiven-llm login\n"},
		{"gateway rejects key", "sk-bad", "aiven-llm: key rejected by gateway (401). Create a key in Aiven Console and run: aiven-llm login\n"},
		{"key grants no models", keyEmpty, "aiven-llm: key grants no models. Create a key in Aiven Console and run: aiven-llm login\n"},
	}
	for _, h := range tableHarnesses {
		for _, tc := range cases {
			t.Run(h+"/"+tc.name, func(t *testing.T) {
				s := newSession(t, newGatewayDouble(t))
				if tc.key != "" {
					s.loggedIn(tc.key)
				}
				r := s.run(h)
				if r.code != 1 || r.stdout != "" || r.stderr != tc.want {
					t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
				}
				if len(s.runner.calls) != 0 {
					t.Errorf("exec'd %v", s.runner.calls)
				}
				for _, f := range s.homeFiles() {
					if filepath.Dir(f) != "aig-keys" {
						t.Errorf("wrote %s", f)
					}
				}
			})
		}
	}
}

func TestTableHarnessesKeepKeyInEnvOnly(t *testing.T) {
	for _, h := range tableHarnesses {
		t.Run(h, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid)
			s.run(h, "--flag")
			c := s.launched()
			for _, a := range c.argv {
				if strings.Contains(a, keyValid) {
					t.Errorf("key in argv %q", c.argv)
				}
			}
			var holders []string
			for _, kv := range c.env {
				if strings.Contains(kv, keyValid) {
					holders = append(holders, kv)
				}
			}
			if want := []string{"AIVEN_GATEWAY_KEY=" + keyValid}; !slices.Equal(holders, want) {
				t.Errorf("env entries holding the key = %q, want %q", holders, want)
			}
			for _, f := range s.homeFiles() {
				if filepath.Dir(f) == "aig-keys" {
					continue
				}
				if data, _ := os.ReadFile(filepath.Join(s.env.Home, f)); strings.Contains(string(data), keyValid) {
					t.Errorf("key written to %s", f)
				}
			}
		})
	}
}

// Claude Code rereads its settings while running, so a launch against another gateway must leave the first one's generated config alone.
func TestLaunchAgainstAnotherGatewayLeavesFirstGatewaysConfig(t *testing.T) {
	for _, args := range [][]string{{"claude"}, {"claude", "--isolated"}, {"codex"}, {"codex", "--isolated"}, {"pi"}, {"pi", "--isolated"}, {"opencode"}, {"omp"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(keyValid).ompPlugin()
			if r := s.run(args...); r.code != 0 {
				t.Fatalf("first launch: exit %d, stderr %q", r.code, r.stderr)
			}
			first := s.generated()
			s.env.BaseURL = newGatewayDouble(t).URL
			s.loggedIn(keyValid).ompPlugin()
			if r := s.run(args...); r.code != 0 {
				t.Fatalf("second launch: exit %d, stderr %q", r.code, r.stderr)
			}
			if len(first) > 0 && len(s.generated()) <= len(first) {
				t.Fatalf("second gateway wrote its config over the first one's")
			}
			for path, data := range first {
				if after, _ := os.ReadFile(path); string(after) != data {
					t.Errorf("second gateway rewrote %s", path)
				}
			}
		})
	}
}

// generated maps every file the launcher wrote outside the key store, OMP's profiles included, to its content.
func (s *session) generated() map[string]string {
	s.t.Helper()
	files := map[string]string{}
	for _, root := range []string{s.env.Home, filepath.Join(s.env.OMPHome, "profiles")} {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Base(filepath.Dir(p)) == "aig-keys" {
				return nil
			}
			data, _ := os.ReadFile(p)
			files[p] = string(data)
			return nil
		})
	}
	return files
}
