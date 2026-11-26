// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"runtime"
	"strings"
	"testing"
)

// cmd.exe expands %VAR% inside PowerShell's single quotes, so on Windows a model id holding % gets no direct command;
// the launch command stays.
func TestDirectModelCommandNeedsPortableLiteral(t *testing.T) {
	for _, tool := range []string{"pi", "opencode", "omp"} {
		s := newSession(t, newGatewayDouble(t)).loggedIn(keyPercent)
		r := s.run("setup", tool, "--yes")
		if r.code != 0 || !strings.Contains(r.stderr, "aiven-ai-gateway launch "+tool) {
			t.Fatalf("%s: exit %d, stderr %q", tool, r.code, r.stderr)
		}
		direct := strings.Contains(r.stderr, "aiven/glm%PATH%")
		if direct != (runtime.GOOS != "windows") {
			t.Errorf("%s on %s: direct command printed %v: %q", tool, runtime.GOOS, direct, r.stderr)
		}
	}
}
