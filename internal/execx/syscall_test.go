// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package execx_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Aiven-Open/ai-gateway-cli/internal/execx"
)

const (
	modeVar    = "AIVEN_EXECX_HELPER"
	sentinel   = "AIVEN_EXECX_SENTINEL=only-via-env"
	parentOnly = "AIVEN_EXECX_PARENT_ONLY=leaked"
)

// Argv and env the exec-mode child hands to Syscall.Exec.
var (
	childArgv = []string{"custom-argv0", "--flag", "two words", ""}
	childEnv  = []string{modeVar + "=print", sentinel, "AIVEN_EXECX_BLANK="}
)

type observed struct {
	Argv []string `json:"argv"`
	Env  []string `json:"env"`
}

func TestSyscallExecReplacesProcessWithExactArgvAndEnv(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), modeVar+"=exec", parentOnly)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("child failed: %v", err)
	}

	var got observed
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if !slices.Equal(got.Argv, childArgv) {
		t.Errorf("argv = %q, want %q", got.Argv, childArgv)
	}
	if !slices.Equal(got.Env, childEnv) {
		t.Errorf("env = %q, want exactly %q (parent env must not leak)", got.Env, childEnv)
	}
	if slices.Contains(got.Argv, sentinel) || strings.Contains(strings.Join(got.Argv, " "), "only-via-env") {
		t.Errorf("sentinel reached argv: %q", got.Argv)
	}
}

func TestSyscallLookPathFindsInstalledBinary(t *testing.T) {
	path, err := execx.Syscall{}.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "/sh") {
		t.Errorf("path = %q, want suffix /sh", path)
	}
}

func TestSyscallLookPathReportsMissingBinary(t *testing.T) {
	_, err := execx.Syscall{}.LookPath("aiven-llm-no-such-harness")
	if err == nil || !strings.Contains(err.Error(), "aiven-llm-no-such-harness is required but not installed") {
		t.Errorf("err = %v", err)
	}
}
