// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package execx_test

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestSyscallExecLauncherOutlivesConsoleInterrupt(t *testing.T) {
	t.Parallel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), modeVar+"=exec-interrupt")
	// A new group lets the break event reach the launcher and harness only.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("harness ready = %q, %v", line, err)
	}
	// Ctrl-C is disabled in a new process group; Ctrl-Break maps to the same os.Interrupt.
	send := syscall.NewLazyDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent")
	if ok, _, err := send.Call(syscall.CTRL_BREAK_EVENT, uintptr(cmd.Process.Pid)); ok == 0 {
		t.Fatalf("GenerateConsoleCtrlEvent: %v", err)
	}
	err = cmd.Wait()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("launcher status = %v, want the harness's 23", err)
	}
}
