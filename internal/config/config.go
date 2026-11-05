// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package config reads the launcher's settings from the process environment.
package config

import (
	"cmp"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

const defaultBaseURL = "https://ai.aiven.io"

// Env is the launcher's configuration.
type Env struct {
	// BaseURL is the gateway origin, normalised by Load: lowercase scheme and host, no trailing slash.
	BaseURL string
	// Keystore is the raw AIVEN_LLM_KEYSTORE mode; keystore.New validates it.
	Keystore string
	// Home holds everything the launcher writes: key records and generated harness config.
	Home string
	// Environ is the process environment, passed on to harnesses.
	Environ []string
	// WorkDir is where project-level harness settings are read from.
	WorkDir string
	// OMPHome is omp's config root (`PI_CONFIG_DIR` under home, default `.omp`), which holds the `aiven-llm` profile.
	OMPHome string
	// PiAgentDir is the user's own pi profile (`PI_CODING_AGENT_DIR`, default `~/.pi/agent`).
	PiAgentDir string
	// CodexHome is the user's own Codex profile (`CODEX_HOME`, default `~/.codex`).
	CodexHome string
}

// Load builds Env from an environ-style KEY=VALUE list (os.Environ()); an invalid AIVEN_LLM_BASE_URL is an error.
func Load(environ []string) (Env, error) {
	env := Env{BaseURL: defaultBaseURL, Keystore: "auto", Environ: environ}
	var home, configDir string
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, "AIVEN_LLM_BASE_URL="); ok && v != "" {
			env.BaseURL = v
		}
		if v, ok := strings.CutPrefix(kv, "AIVEN_LLM_KEYSTORE="); ok && v != "" {
			env.Keystore = v
		}
		if v, ok := strings.CutPrefix(kv, "AIVEN_LLM_HOME="); ok && v != "" {
			env.Home = v
		}
		if v, ok := strings.CutPrefix(kv, "PI_CONFIG_DIR="); ok && v != "" {
			configDir = v
		}
		if v, ok := strings.CutPrefix(kv, "PI_CODING_AGENT_DIR="); ok && v != "" {
			env.PiAgentDir = v
		}
		if v, ok := strings.CutPrefix(kv, "CODEX_HOME="); ok && v != "" {
			env.CodexHome = v
		}
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			home = v
		}
	}
	if home != "" {
		env.PiAgentDir = cmp.Or(expandTilde(env.PiAgentDir, home), filepath.Join(home, ".pi", "agent"))
		env.CodexHome = cmp.Or(env.CodexHome, filepath.Join(home, ".codex"))
	}
	if env.Home == "" && home != "" {
		env.Home = home + "/.config/aiven-llm"
	}
	if home != "" {
		// omp joins PI_CONFIG_DIR (default ".omp") onto the home directory, absolute values included.
		if configDir == "" {
			configDir = ".omp"
		}
		env.OMPHome = filepath.Join(home, configDir)
	}
	base, err := normaliseBaseURL(env.BaseURL)
	if err != nil {
		return Env{}, err
	}
	env.BaseURL = base
	return env, nil
}

// expandTilde resolves a leading `~` as pi does for PI_CODING_AGENT_DIR.
func expandTilde(path, home string) string {
	if path == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return path
}

// normaliseBaseURL accepts only an http(s) origin, so every endpoint appends its own path to one canonical value.
func normaliseBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("invalid AIVEN_LLM_BASE_URL %q: want http(s)://host[:port]", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("invalid AIVEN_LLM_BASE_URL %q: no credentials, path, query or fragment allowed", raw)
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), nil
}
