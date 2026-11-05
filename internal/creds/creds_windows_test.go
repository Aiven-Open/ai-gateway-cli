// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package creds_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

func checkPrivateKeyFile(t *testing.T, path string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("key file control = %#x, %v; want SE_DACL_PROTECTED", control, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	// Compared by SID rather than as SDDL text, which abbreviates well-known accounts: the built-in Administrator a CI
	// runner runs as is LA. 0x1f01ff is FA, what the GENERIC_ALL the DACL grants maps to for a file.
	var ace *windows.ACCESS_ALLOWED_ACE
	if dacl.AceCount != 1 || windows.GetAce(dacl, 0, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
		ace.Mask != 0x1f01ff || !(*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(user.User.Sid) {
		t.Errorf("key file SDDL = %q, want a single FA entry for %s", sd.String(), user.User.Sid)
	}
}

func TestFromEnvMatchesEnvVarCaseInsensitivelyOnWindows(t *testing.T) {
	if got := creds.FromEnv([]string{"aiven_ai_gateway_key=sk-test-win"}); got != "sk-test-win" {
		t.Fatalf("FromEnv = %q, want sk-test-win", got)
	}
}

// The PowerShell helper prints the key file's content and fails without it, whatever the environment holds, whether run
// directly (as Codex runs it) or through cmd.exe (as Claude Code runs it).
func TestPowerShellHelperReadsTheKey(t *testing.T) {
	name, args := creds.Command(other, "windows")
	for runner, makeCmd := range map[string]func(context.Context, *testing.T) *exec.Cmd{
		"direct": func(ctx context.Context, _ *testing.T) *exec.Cmd { return exec.CommandContext(ctx, name, args...) },
		// Node's spawn with shell: true, which Claude Code uses, passes this command line verbatim, and Claude Code's
		// exec wrapper leaves stdin an open pipe when it has no input.
		"cmd.exe": func(ctx context.Context, t *testing.T) *exec.Cmd {
			cmd := exec.CommandContext(ctx, "cmd.exe")
			cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /s /c "` + creds.CommandLine(other, "windows") + `"`}
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
			cmd.Stdin = r
			return cmd
		},
	} {
		t.Run(runner, func(t *testing.T) {
			home := t.TempDir()
			run := func(environ ...string) (string, error) {
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				defer cancel()
				cmd := makeCmd(ctx, t)
				cmd.Env = append(os.Environ(), append([]string{"USERPROFILE=" + home, "AIVEN_AI_GATEWAY_KEY="}, environ...)...)
				out, err := cmd.Output()
				return strings.TrimSpace(string(out)), err
			}
			if out, err := run(); err == nil {
				t.Errorf("no key: %q, want a failure", out)
			}
			if out, err := run("AIVEN_AI_GATEWAY_KEY=sk-env"); err == nil || out != "" {
				t.Errorf("environment key only: %q, %v; want a failure", out, err)
			}
			if _, err := creds.Write(home, other, key, fsutil.WriteAtomic); err != nil {
				t.Fatal(err)
			}
			if out, err := run("AIVEN_AI_GATEWAY_KEY=sk-env"); err != nil || out != key {
				t.Errorf("key file: %q, %v", out, err)
			}
		})
	}
}
