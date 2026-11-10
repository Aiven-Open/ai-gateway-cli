// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"os"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

// The black-box tests cannot reach the default gateway, so this one checks its Console key file directly, beside a
// key saved for another gateway: each gateway reads its own file only, never the environment's key.
func TestStoredKeyReadsOnlyTheConfiguredGatewaysKeyFile(t *testing.T) {
	a := config.Env{BaseURL: config.DefaultBaseURL, UserHome: t.TempDir(), Environ: []string{creds.EnvVar + "=sk-env"}}
	b := a
	b.BaseURL = "https://b.example"
	if _, err := creds.Write(a.UserHome, a.BaseURL, "sk-console", fsutil.WriteAtomic); err != nil {
		t.Fatal(err)
	}
	if key, err := storedKey(a); err != nil || key != "sk-console" {
		t.Fatalf("default gateway: %q, %v", key, err)
	}
	if key, err := storedKey(b); err != nil || key != "" {
		t.Fatalf("other gateway before its login: %q, %v; want none", key, err)
	}
	if _, err := saveKey(b, Deps{}, "sk-b"); err != nil {
		t.Fatal(err)
	}
	for env, want := range map[*config.Env]string{&a: "sk-console", &b: "sk-b"} {
		if key, err := storedKey(*env); err != nil || key != want {
			t.Errorf("%s after a login to b: %q, %v; want %q", env.BaseURL, key, err, want)
		}
	}
	if err := removeKey(b, Deps{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(creds.Path(a.UserHome, a.BaseURL)); err != nil {
		t.Errorf("logout from b removed the default gateway's key file: %v", err)
	}
}
