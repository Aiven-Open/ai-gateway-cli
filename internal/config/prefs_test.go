// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
)

func TestModelPrefsRoundTripAndPerGatewayIsolation(t *testing.T) {
	t.Parallel()
	home := t.TempDir()

	// Missing file returns empty cleanly.
	got, err := config.LoadModelPref(home, config.DefaultBaseURL, "pi")
	if err != nil || got != "" {
		t.Fatalf("LoadModelPref(missing) = %q, %v; want empty, nil", got, err)
	}

	// Empty updates do not create the file.
	if err := config.SaveModelPrefs(home, config.DefaultBaseURL, nil, nil); err != nil {
		t.Fatalf("SaveModelPrefs(empty) = %v", err)
	}
	if _, err := os.Stat(config.ModelPrefsPath(home)); !os.IsNotExist(err) {
		t.Fatalf("empty SaveModelPrefs created file: %v", err)
	}

	// Save for two gateways and verify permissions and isolation.
	otherGW := "https://gw.example:8443"
	if err := config.SaveModelPrefs(
		home,
		config.DefaultBaseURL,
		map[string]string{"pi": "glm-5", "opencode": "deepseek-v3.2"},
		nil,
	); err != nil {
		t.Fatalf("SaveModelPrefs(default) = %v", err)
	}
	if err := config.SaveModelPrefs(
		home,
		otherGW,
		map[string]string{"pi": "claude-sonnet-5-5"},
		nil,
	); err != nil {
		t.Fatalf("SaveModelPrefs(other) = %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(config.ModelPrefsPath(home))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %o, want 0600", info.Mode().Perm())
		}
	}
	for _, tc := range []struct{ origin, tool, want string }{
		{config.DefaultBaseURL, "pi", "glm-5"},
		{config.DefaultBaseURL, "opencode", "deepseek-v3.2"},
		{config.DefaultBaseURL, "omp", ""},
		{otherGW, "pi", "claude-sonnet-5-5"},
		{otherGW, "opencode", ""},
	} {
		if got, err := config.LoadModelPref(home, tc.origin, tc.tool); err != nil || got != tc.want {
			t.Errorf("LoadModelPref(%s, %s) = %q, %v; want %q", tc.origin, tc.tool, got, err, tc.want)
		}
	}

	// Clearing an entry deletes only that tool, preserving others.
	if err := config.SaveModelPrefs(
		home,
		config.DefaultBaseURL,
		map[string]string{"pi": ""},
		nil,
	); err != nil {
		t.Fatalf("SaveModelPrefs(clear) = %v", err)
	}
	if got, err := config.LoadModelPref(home, config.DefaultBaseURL, "pi"); err != nil || got != "" {
		t.Errorf("cleared pi = %q, %v; want empty", got, err)
	}
	if got, err := config.LoadModelPref(
		home,
		config.DefaultBaseURL,
		"opencode",
	); err != nil ||
		got != "deepseek-v3.2" {
		t.Errorf("opencode after clear = %q, %v; want deepseek-v3.2", got, err)
	}
}

func TestModelPrefsErrors(t *testing.T) {
	t.Parallel()
	if _, err := config.LoadModelPrefs(""); !errors.Is(err, config.ErrNoHome) {
		t.Errorf("LoadModelPrefs('') = %v, want ErrNoHome", err)
	}
	home := t.TempDir()
	path := config.ModelPrefsPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadModelPrefs(home); err == nil {
		t.Error("LoadModelPrefs(invalid JSON) succeeded, want error")
	}
}
