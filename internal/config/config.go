// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package config reads the launcher's settings from the process environment.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// DefaultBaseURL is the production gateway.
const DefaultBaseURL = "https://ai.aiven.io"

// Env is the launcher's configuration.
type Env struct {
	// BaseURL is the gateway origin, normalised by Load: lowercase scheme and host, no trailing slash.
	BaseURL string
	// Environ is the process environment, passed on to harnesses.
	Environ []string
	// WorkDir is where project-level harness settings are read from.
	WorkDir string
	// UserHome is the user's home directory, from HomeVar, which holds the key files and the tools' own configuration.
	UserHome string
	// ConfigHome is XDG_CONFIG_HOME, which roots OpenCode's configuration when it is not empty.
	ConfigHome string
}

// ErrNoHome is returned when the environment names no home directory.
var ErrNoHome = errors.New("cannot locate a home directory. Set " + HomeVar)

// Load builds Env from an environ-style KEY=VALUE list (os.Environ()); an invalid AIVEN_AI_GATEWAY_BASE_URL is an error.
func Load(environ []string) (Env, error) {
	env := Env{BaseURL: DefaultBaseURL, Environ: environ}
	var home string
	for _, kv := range environ {
		name, v, _ := strings.Cut(kv, "=")
		if SameVar(name, "AIVEN_AI_GATEWAY_BASE_URL") && v != "" {
			env.BaseURL = v
		}
		if SameVar(name, HomeVar) {
			home = v
		}
		if SameVar(name, "XDG_CONFIG_HOME") {
			env.ConfigHome = v
		}
	}
	env.UserHome = home
	base, err := normaliseBaseURL(env.BaseURL)
	if err != nil {
		return Env{}, err
	}
	env.BaseURL = base
	return env, nil
}

// normaliseBaseURL accepts only an HTTPS origin, so every endpoint appends its own path to one canonical value.
func normaliseBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err == nil && u.Scheme == "http" {
		return "", fmt.Errorf("invalid AIVEN_AI_GATEWAY_BASE_URL: HTTPS is required because HTTP exposes bearer credentials")
	}
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", fmt.Errorf("invalid AIVEN_AI_GATEWAY_BASE_URL: want an HTTPS origin with a host and optional port")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("invalid AIVEN_AI_GATEWAY_BASE_URL: no credentials, path, query or fragment allowed")
	}
	// A default port names the same gateway, which must keep one key file.
	host := u.Host
	if u.Port() == "443" {
		host = strings.TrimSuffix(host, ":"+u.Port())
	}
	return strings.ToLower(u.Scheme + "://" + host), nil
}
