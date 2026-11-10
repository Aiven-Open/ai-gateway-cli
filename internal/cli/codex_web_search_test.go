// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// The user's canonical web-search setting is kept even when Aiven becomes the native default.
func TestCodexWebSearchDisabledInTheAivenProfileOnly(t *testing.T) {
	for _, asDefault := range []bool{false, true} {
		t.Run(map[bool]string{false: "profile only", true: "native default"}[asDefault], func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
			base := filepath.Join(s.fakeHome, ".codex/config.toml")
			profile := filepath.Join(s.fakeHome, ".codex/aiven.config.toml")
			writeFile(t, base, "model='other-model'\nweb_search='live'\n")
			writeFile(t, profile, "model='claude-opus'\nweb_search='cached'\n")
			args := []string{"setup", "codex", "--key-stdin", "--yes"}
			if asDefault {
				args = args[:3]
				s.deps.Prompter = &answerPrompter{answer: "y"}
			}
			if r := s.stdin(keyValid).run(args...); r.code != 0 {
				t.Fatalf("%d %s", r.code, r.stderr)
			}
			var canonical, dedicated map[string]any
			codexFile(t, s, "config.toml", &canonical)
			codexFile(t, s, "aiven.config.toml", &dedicated)
			if dedicated["web_search"] != "disabled" || dedicated["model"] != "claude-opus" {
				t.Fatalf("profile setting or preserved pin: %v", dedicated)
			}
			if canonical["web_search"] != "live" || asDefault != (canonical["model_provider"] == "aiven") {
				t.Fatalf("native-default=%v: canonical %v, want web_search=live", asDefault, canonical)
			}
			beforeBase, _ := os.ReadFile(base)
			beforeProfile, _ := os.ReadFile(profile)
			if r := s.run("launch", "codex", "--", "-c", `web_search="live"`); r.code != 0 {
				t.Fatalf("%d %s", r.code, r.stderr)
			}
			if got := s.launched().argv; !slices.Equal(got, []string{"codex", "-p", "aiven", "-c", `web_search="live"`}) {
				t.Fatalf("native override changed: %q", got)
			}
			afterBase, _ := os.ReadFile(base)
			afterProfile, _ := os.ReadFile(profile)
			if !reflect.DeepEqual(beforeBase, afterBase) || !reflect.DeepEqual(beforeProfile, afterProfile) {
				t.Fatal("launch rewrote the web-search policy")
			}
			if r := s.stdin(keyValid).run(args...); r.code != 0 {
				t.Fatalf("rerun %d %s", r.code, r.stderr)
			}
			canonical, dedicated = nil, nil
			codexFile(t, s, "config.toml", &canonical)
			codexFile(t, s, "aiven.config.toml", &dedicated)
			if dedicated["web_search"] != "disabled" || canonical["web_search"] != "live" {
				t.Fatalf("rerun: profile web_search %v, canonical %v", dedicated["web_search"], canonical["web_search"])
			}
		})
	}
}

func TestCodexWebSearchDisabledForEverySelectedModel(t *testing.T) {
	for _, key := range []string{keyValid, keyClaudeOnly, keyFlash, keyProduction} {
		t.Run(key, func(t *testing.T) {
			s := newSession(t, newGatewayDouble(t)).loggedIn(key)
			if r := s.run("setup", "codex", "--yes"); r.code != 0 {
				t.Fatalf("%d %s", r.code, r.stderr)
			}
			var profile map[string]any
			codexFile(t, s, "aiven.config.toml", &profile)
			if profile["web_search"] != "disabled" {
				t.Fatalf("model %v retains hosted web search", profile["model"])
			}
		})
	}
}
