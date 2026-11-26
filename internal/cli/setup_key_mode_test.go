// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

func TestSetupRestoresKeyFileModeWhenKeyIsOtherwiseUnchanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix permission bits; the key file's access follows the profile directory")
	}
	srv := hookedGateway(t, nil, nil)
	env, _ := inlineSetupEnv(t, srv.URL)
	if _, err := creds.Write(env.UserHome, env.BaseURL, "sk-saved", fsutil.WriteAtomic); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	deps := inlineDeps(&scriptedSelector{}, &stderr)
	deps.HTTP = srv.Client()
	if code := Run(t.Context(), []string{"setup", "claude", "--yes"}, env, deps); code != 0 {
		t.Fatalf("first setup: exit %d:\n%s", code, stderr.String())
	}
	path := creds.Path(env.UserHome, env.BaseURL)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	code := Run(t.Context(), []string{"setup", "claude", "--yes"}, env, deps)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if code != 0 || info.Mode().Perm() != 0o600 || string(data) != "sk-saved\n" {
		t.Errorf("exit %d, mode %v, key file %q:\n%s", code, info.Mode().Perm(), data, stderr.String())
	}
}
