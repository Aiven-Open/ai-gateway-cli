// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package config_test

import (
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
)

// Windows variable names ignore case; Load must find the profile and gateway overrides however the environment spells them.
func TestUserProfileIgnoresCase(t *testing.T) {
	env, err := config.Load([]string{
		`UserProfile=C:\Users\ada`,
		`aiven_ai_gateway_base_url=https://127.0.0.1:12345`,
		`xdg_config_home=C:\Users\ada\xdg`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if env.UserHome != `C:\Users\ada` {
		t.Errorf("UserHome = %q, want C:\\Users\\ada", env.UserHome)
	}
	if env.BaseURL != "https://127.0.0.1:12345" {
		t.Errorf("BaseURL = %q, want https://127.0.0.1:12345", env.BaseURL)
	}
	if env.ConfigHome != `C:\Users\ada\xdg` {
		t.Errorf("ConfigHome = %q, want C:\\Users\\ada\\xdg", env.ConfigHome)
	}
}
