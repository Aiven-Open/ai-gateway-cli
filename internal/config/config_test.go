// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package config_test

import (
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
)

func load(t *testing.T, environ []string) config.Env {
	t.Helper()
	env, err := config.Load(environ)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestBaseURLDefaultsToAivenGateway(t *testing.T) {
	env := load(t, []string{"PATH=/bin"})
	if env.BaseURL != "https://ai.aiven.io" {
		t.Fatalf("BaseURL = %q, want https://ai.aiven.io", env.BaseURL)
	}
}

func TestHTTPGatewayIsRejectedBecauseItExposesBearerCredentials(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:4000", "http://gateway.example:8443", "HTTP://[::1]"} {
		t.Run(raw, func(t *testing.T) {
			_, err := config.Load([]string{"AIVEN_AI_GATEWAY_BASE_URL=" + raw})
			if err == nil || !strings.Contains(err.Error(), "HTTPS") || !strings.Contains(err.Error(), "bearer") {
				t.Fatalf("Load(%q) error = %v; want an HTTPS and bearer credential explanation", raw, err)
			}
		})
	}
}

func TestBaseURLOverriddenByEnvironment(t *testing.T) {
	env := load(t, []string{"AIVEN_AI_GATEWAY_BASE_URL=https://127.0.0.1:4000"})
	if env.BaseURL != "https://127.0.0.1:4000" {
		t.Fatalf("BaseURL = %q, want the override", env.BaseURL)
	}
}

func TestConfigHomeIsXDGConfigHome(t *testing.T) {
	if env := load(t, []string{"XDG_CONFIG_HOME=/home/ada/xdg"}); env.ConfigHome != "/home/ada/xdg" {
		t.Errorf("ConfigHome = %q, want /home/ada/xdg", env.ConfigHome)
	}
	if env := load(t, []string{"HOME=/home/ada"}); env.ConfigHome != "" {
		t.Errorf("ConfigHome = %q without XDG_CONFIG_HOME", env.ConfigHome)
	}
}

func TestUserHomeIsHomeVar(t *testing.T) {
	env := load(t, []string{config.HomeVar + "=/home/ada"})
	if env.UserHome != "/home/ada" {
		t.Errorf("UserHome = %q, want /home/ada", env.UserHome)
	}
}

// The other platform's variable is not a home: on Windows, Git Bash sets a HOME the Node tools do not read.
func TestUserHomeIgnoresOtherPlatformsVar(t *testing.T) {
	other := map[string]string{"HOME": "USERPROFILE", "USERPROFILE": "HOME"}[config.HomeVar]
	if env := load(t, []string{other + "=/home/ada"}); env.UserHome != "" {
		t.Errorf("UserHome = %q from %s, want none", env.UserHome, other)
	}
	if !strings.Contains(config.ErrNoHome.Error(), "Set "+config.HomeVar) {
		t.Errorf("ErrNoHome = %q, want it to name %s", config.ErrNoHome, config.HomeVar)
	}
}

func TestBaseURLIsNormalised(t *testing.T) {
	for raw, want := range map[string]string{
		"https://ai.aiven.io/":     "https://ai.aiven.io",
		"  HTTPS://AI.Aiven.IO  ":  "https://ai.aiven.io",
		"https://127.0.0.1:4000/":  "https://127.0.0.1:4000",
		"https://ai.aiven.io:8443": "https://ai.aiven.io:8443",
		"https://ai.aiven.io:443":  "https://ai.aiven.io",
		"HTTPS://AI.AIVEN.IO:443/": "https://ai.aiven.io",
		"https://[::1]:443":        "https://[::1]",
		"https://ai.aiven.io:80":   "https://ai.aiven.io:80",
	} {
		if got := load(t, []string{"AIVEN_AI_GATEWAY_BASE_URL=" + raw}).BaseURL; got != want {
			t.Errorf("BaseURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestInvalidBaseURLIsRejected(t *testing.T) {
	for _, raw := range []string{
		"https://ai.aiven.io/v1", "https://ai.aiven.io/x/", "https://ai.aiven.io?a=b", "https://ai.aiven.io/?a=b",
		"https://ai.aiven.io#frag", "https://user:s3cret@ai.aiven.io", "ftp://ai.aiven.io", "ai.aiven.io", "https://",
	} {
		_, err := config.Load([]string{"AIVEN_AI_GATEWAY_BASE_URL=" + raw})
		if err == nil || !strings.Contains(err.Error(), "AIVEN_AI_GATEWAY_BASE_URL") || strings.Contains(err.Error(), raw) ||
			strings.Contains(err.Error(), "s3cret") {
			t.Errorf("Load(%q) err = %v, want an AIVEN_AI_GATEWAY_BASE_URL error without the value", raw, err)
		}
	}
}
