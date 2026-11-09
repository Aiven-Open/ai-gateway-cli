// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package config_test

import (
	"strings"
	"testing"

	"github.com/Aiven-Open/ai-gateway-cli/internal/config"
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

func TestBaseURLOverriddenByEnvironment(t *testing.T) {
	env := load(t, []string{"AIVEN_LLM_BASE_URL=http://127.0.0.1:4000"})
	if env.BaseURL != "http://127.0.0.1:4000" {
		t.Fatalf("BaseURL = %q, want the override", env.BaseURL)
	}
}

func TestHomeDefaultsUnderUserConfigDir(t *testing.T) {
	env := load(t, []string{"HOME=/home/ada"})
	if env.Home != "/home/ada/.config/aiven-llm" {
		t.Fatalf("Home = %q", env.Home)
	}
}

func TestHomeOverriddenByEnvironment(t *testing.T) {
	env := load(t, []string{"HOME=/home/ada", "AIVEN_LLM_HOME=/srv/llm"})
	if env.Home != "/srv/llm" {
		t.Fatalf("Home = %q, want the override", env.Home)
	}
}

func TestOMPHomeDefaultsToDotOmpUnderHome(t *testing.T) {
	env := load(t, []string{"HOME=/home/ada"})
	if env.OMPHome != "/home/ada/.omp" {
		t.Fatalf("OMPHome = %q", env.OMPHome)
	}
}

// omp joins PI_CONFIG_DIR onto the home directory, absolute values included.
func TestOMPHomeFollowsPiConfigDir(t *testing.T) {
	for dir, want := range map[string]string{".omp-alt": "/home/ada/.omp-alt", "/srv/omp": "/home/ada/srv/omp"} {
		env := load(t, []string{"HOME=/home/ada", "PI_CONFIG_DIR=" + dir})
		if env.OMPHome != want {
			t.Errorf("PI_CONFIG_DIR=%s: OMPHome = %q, want %q", dir, env.OMPHome, want)
		}
	}
}

func TestOMPHomeIgnoresOmpHome(t *testing.T) {
	env := load(t, []string{"HOME=/home/ada", "OMP_HOME=/srv/omp"})
	if env.OMPHome != "/home/ada/.omp" {
		t.Fatalf("OMPHome = %q, want the default", env.OMPHome)
	}
}

func TestPiAgentDirDefaultsUnderHomeAndFollowsPiCodingAgentDir(t *testing.T) {
	if got := load(t, []string{"HOME=/home/ada"}).PiAgentDir; got != "/home/ada/.pi/agent" {
		t.Errorf("PiAgentDir = %q", got)
	}
	if got := load(t, []string{"HOME=/home/ada", "PI_CODING_AGENT_DIR=/srv/pi"}).PiAgentDir; got != "/srv/pi" {
		t.Errorf("PiAgentDir = %q, want PI_CODING_AGENT_DIR", got)
	}
	// pi expands a leading ~ itself.
	if got := load(t, []string{"HOME=/home/ada", "PI_CODING_AGENT_DIR=~/pi"}).PiAgentDir; got != "/home/ada/pi" {
		t.Errorf("PiAgentDir = %q, want ~ expanded", got)
	}
}

func TestCodexHomeDefaultsUnderHomeAndFollowsCodexHome(t *testing.T) {
	if got := load(t, []string{"HOME=/home/ada"}).CodexHome; got != "/home/ada/.codex" {
		t.Errorf("CodexHome = %q", got)
	}
	if got := load(t, []string{"HOME=/home/ada", "CODEX_HOME=/srv/codex"}).CodexHome; got != "/srv/codex" {
		t.Errorf("CodexHome = %q, want CODEX_HOME", got)
	}
}

func TestKeystoreModeDefaultsToAutoAndIsOverridable(t *testing.T) {
	if got := load(t, []string{"PATH=/bin"}).Keystore; got != "auto" {
		t.Errorf("default Keystore = %q, want auto", got)
	}
	if got := load(t, []string{"AIVEN_LLM_KEYSTORE=file"}).Keystore; got != "file" {
		t.Errorf("Keystore = %q, want file", got)
	}
}

func TestBaseURLIsNormalised(t *testing.T) {
	for raw, want := range map[string]string{
		"https://ai.aiven.io/":     "https://ai.aiven.io",
		"  HTTPS://AI.Aiven.IO  ":  "https://ai.aiven.io",
		"http://127.0.0.1:4000/":   "http://127.0.0.1:4000",
		"https://ai.aiven.io:8443": "https://ai.aiven.io:8443",
	} {
		if got := load(t, []string{"AIVEN_LLM_BASE_URL=" + raw}).BaseURL; got != want {
			t.Errorf("BaseURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestInvalidBaseURLIsRejected(t *testing.T) {
	for _, raw := range []string{
		"https://ai.aiven.io/v1", "https://ai.aiven.io/x/", "https://ai.aiven.io?a=b", "https://ai.aiven.io/?a=b",
		"https://ai.aiven.io#frag", "https://user:pw@ai.aiven.io", "ftp://ai.aiven.io", "ai.aiven.io", "https://",
	} {
		_, err := config.Load([]string{"AIVEN_LLM_BASE_URL=" + raw})
		if err == nil || !strings.Contains(err.Error(), "AIVEN_LLM_BASE_URL") {
			t.Errorf("Load(%q) err = %v, want AIVEN_LLM_BASE_URL error", raw, err)
		}
	}
}

func TestXDGDataHomeReadFromEnvironment(t *testing.T) {
	if env := load(t, []string{"HOME=/home/ada", "XDG_DATA_HOME=/home/ada/.data"}); env.XDGDataHome != "/home/ada/.data" {
		t.Errorf("XDGDataHome = %q", env.XDGDataHome)
	}
	if env := load(t, []string{"HOME=/home/ada"}); env.XDGDataHome != "" {
		t.Errorf("XDGDataHome = %q, want empty", env.XDGDataHome)
	}
}
