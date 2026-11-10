// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package execx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/execx"
)

const (
	modeVar    = "AIVEN_EXECX_HELPER"
	sentinel   = "AIVEN_EXECX_SENTINEL=only-via-env"
	parentOnly = "AIVEN_EXECX_PARENT_ONLY=leaked"
)

// Argv and env the exec-mode child hands to Syscall.Exec.
var (
	childArgv      = []string{"custom-argv0", "--flag", "two words", "", `quote"slash\`, "$HOME; & | *", "日本語"}
	batchChildArgv = append(append([]string{}, childArgv...),
		"--settings",
		`{"apiKeyHelper":"sh -c '[ -f ~/.config/aiven/ai-gateway-key ] && cat ~/.config/aiven/ai-gateway-key || printf %s \"$AIVEN_AI_GATEWAY_KEY\"'","env":{"ANTHROPIC_BASE_URL":"https://ai.aiven.io"}}`,
		"a & b | c < d > e (f) ^ %USERPROFILE%",
	)
	childEnv = []string{modeVar + "=print", sentinel, "AIVEN_EXECX_BLANK="}
)

type observed struct {
	Argv      []string `json:"argv"`
	Env       []string `json:"env"`
	Input     string   `json:"input"`
	PID       int      `json:"pid"`
	ParentPID int      `json:"parentPID"`
}

func TestSyscallExecHandsOffArgvEnvStreamsAndStatus(t *testing.T) {
	t.Parallel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), modeVar+"=exec", parentOnly)
	cmd.Stdin = strings.NewReader("inherited stdin")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("child status = %v, want 23; stderr = %s", err, stderr.String())
	}

	var got observed
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if !slices.Equal(got.Argv, childArgv) {
		t.Errorf("argv = %q, want %q", got.Argv, childArgv)
	}
	var payloadEnv []string
	for _, kv := range got.Env {
		if strings.HasPrefix(kv, "AIVEN_EXECX_PARENT_PID=") ||
			(runtime.GOOS == "windows" && strings.HasPrefix(strings.ToUpper(kv), "SYSTEMROOT=")) {
			continue
		}
		payloadEnv = append(payloadEnv, kv)
	}
	slices.Sort(payloadEnv)
	wantEnv := append([]string{}, childEnv...)
	slices.Sort(wantEnv)
	if !slices.Equal(payloadEnv, wantEnv) {
		t.Errorf("env = %q, want exactly %q (parent env must not leak)", got.Env, childEnv)
	}
	if got.Input != "inherited stdin" || stderr.String() != "inherited stderr" {
		t.Errorf("streams = %q, %q", got.Input, stderr.String())
	}
	if got.ParentPID == 0 || (got.PID == got.ParentPID) != (runtime.GOOS != "windows") {
		t.Errorf("handoff PID = %d, original = %d on %s", got.PID, got.ParentPID, runtime.GOOS)
	}
	if slices.Contains(got.Argv, sentinel) || strings.Contains(strings.Join(got.Argv, " "), "only-via-env") {
		t.Errorf("sentinel reached argv: %q", got.Argv)
	}
}

func TestSyscallLookPathFindsInstalledBinary(t *testing.T) {
	t.Parallel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path, err := execx.Syscall{}.LookPath(exe)
	if err != nil {
		t.Fatal(err)
	}
	if path != exe {
		t.Errorf("path = %q, want %q", path, exe)
	}
}

func TestSyscallLookPathReportsMissingBinary(t *testing.T) {
	t.Parallel()
	_, err := execx.Syscall{}.LookPath("aiven-ai-gateway-no-such-harness")
	if err == nil || !strings.Contains(err.Error(), "aiven-ai-gateway-no-such-harness is required but not installed") {
		t.Errorf("err = %v", err)
	}
}

func TestSyscallExecMissingBinaryReturnsFailure(t *testing.T) {
	t.Parallel()
	err := execx.Syscall{}.Exec("aiven-ai-gateway-no-such-harness", []string{"synthetic-secret"}, nil)
	if err == nil || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatalf("error = %v", err)
	}
}

func TestSyscallExecExitsZeroWithoutResumingLauncher(t *testing.T) {
	t.Parallel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), modeVar+"=exec-zero")
	out, err := cmd.Output()
	if err != nil || string(out) != "done" {
		t.Fatalf("child = %q, %v", out, err)
	}
}

func TestSyscallExecHandsOffBatchShimOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("runs cmd.exe on a Windows .cmd shim")
	}
	t.Parallel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	shim := filepath.Join(dir, "aiven-shim.cmd")
	// Matches the wrapper npm's cmd-shim writes for global bins on Windows.
	body := "@ECHO off\r\n" +
		"GOTO start\r\n" +
		":find_dp0\r\n" +
		"SET dp0=%~dp0\r\n" +
		"EXIT /b\r\n" +
		":start\r\n" +
		"SETLOCAL\r\n" +
		"CALL :find_dp0\r\n" +
		"SET \"_prog=" + exe + "\"\r\n" +
		"endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & \"%_prog%\" %*\r\n"
	if err := os.WriteFile(shim, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(),
		modeVar+"=exec-batch",
		"AIVEN_EXECX_BATCH_TARGET=aiven-shim",
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"USERPROFILE=C:\\Users\\unexpanded",
		parentOnly,
	)
	cmd.Stdin = strings.NewReader("inherited stdin")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("child status = %v, want 23; stderr = %s", err, stderr.String())
	}
	var got observed
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if !slices.Equal(got.Argv[1:], batchChildArgv[1:]) {
		t.Errorf("argv[1:] = %q, want %q", got.Argv[1:], batchChildArgv[1:])
	}
	if got.Input != "inherited stdin" || stderr.String() != "inherited stderr" {
		t.Errorf("streams = %q, %q", got.Input, stderr.String())
	}
	if slices.Contains(got.Env, parentOnly) || !slices.Contains(got.Env, sentinel) {
		t.Errorf("env = %q, want %q without %q", got.Env, sentinel, parentOnly)
	}
}

func TestSyscallExecStartFailureDoesNotExposeArgv(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "invalid.exe")
	if err := os.WriteFile(file, []byte("invalid native executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := execx.Syscall{}.Exec(file, []string{file, "synthetic-secret"}, nil)
	if err == nil || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatalf("error = %v", err)
	}
	if runtime.GOOS == "windows" {
		shim := filepath.Join(t.TempDir(), "shim.cmd")
		if err := os.WriteFile(shim, []byte("@echo off\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		err := execx.Syscall{}.Exec(shim, []string{shim, "synthetic-secret\ninjected"}, nil)
		if err == nil || strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatalf("batch error = %v", err)
		}
	}
}
