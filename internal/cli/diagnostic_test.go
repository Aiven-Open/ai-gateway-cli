// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestFailureDiagnosticsKeepTextWithoutTerminalControls(t *testing.T) {
	t.Parallel()
	var out, stderr bytes.Buffer
	code := fail(Deps{Stdout: &out, Stderr: &stderr}, "test request to private-\x1b[31m模型\x1b[0m\u202e: server error")
	if code != 1 || out.Len() != 0 || strings.ContainsAny(stderr.String(), "\x1b\u202e") ||
		!strings.Contains(stderr.String(), "模型") || !strings.Contains(stderr.String(), "server error") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
}

func TestFailureDiagnosticsKeepJoinedErrorsApart(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	fail(Deps{Stderr: &stderr}, errors.Join(errors.New("write key"), errors.New("put back the previous key origin")).Error())
	if got, want := stderr.String(), "aiven-ai-gateway: write key; put back the previous key origin\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}
