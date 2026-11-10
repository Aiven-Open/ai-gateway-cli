// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package creds_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

const (
	key   = "AVNS_test.fixture00000000wxyz"
	other = "https://gw.example:8443"
)

func TestNameIsASlugAndAHashOfTheOrigin(t *testing.T) {
	for origin, want := range map[string]string{
		other:                    "https-gw.example-8443-9db703938f0f",
		"http://[::1]:8080":      "http-1-8080-23f440d84221",
		"http://127.0.0.1:41234": "http-127.0.0.1-41234-4c466522f2c5",
		"https://a-very-long-gateway-hostname.example:8443": "https-a-very-long-gateway-hostname.examp-c44a3399829f",
		// The cap lands on a dot, which is trimmed as at either end.
		"https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.b.io": "https-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-e2de9837f911",
	} {
		if got := creds.Name(origin); got != want {
			t.Errorf("Name(%q) = %q, want %q", origin, got, want)
		}
	}
}

func TestPathKeepsTheConsoleKeyFileForTheDefaultGateway(t *testing.T) {
	home := t.TempDir()
	if got, want := creds.Path(home, config.DefaultBaseURL), filepath.Join(home, ".config", "aiven", "ai-gateway-key"); got != want {
		t.Errorf("default gateway: Path = %q, want %q", got, want)
	}
	want := filepath.Join(home, ".config", "aiven", "ai-gateway-keys", "https-gw.example-8443-9db703938f0f")
	if got := creds.Path(home, other); got != want {
		t.Errorf("other gateway: Path = %q, want %q", got, want)
	}
}

func TestWriteSavesTheBareKeyPrivately(t *testing.T) {
	for _, origin := range []string{config.DefaultBaseURL, other} {
		home := t.TempDir()
		path, err := creds.Write(home, origin, key, fsutil.WriteAtomic)
		if err != nil || path != creds.Path(home, origin) {
			t.Fatalf("Write = %q, %v", path, err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != key+"\n" {
			t.Fatalf("content = %q, %v", data, err)
		}
		checkPrivateKeyFile(t, path)
	}
}

// A key file that is a symlink is replaced, so the key never lands in, or loosens, the file it points to.
func TestWriteReplacesASymlinkedKeyFile(t *testing.T) {
	home := t.TempDir()
	victim := filepath.Join(home, "victim")
	if err := os.WriteFile(victim, []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := creds.Path(home, other)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, path); err != nil {
		t.Skip(err)
	}
	if _, err := creds.Write(home, other, key, fsutil.WriteAtomic); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(victim); string(data) != "other\n" {
		t.Errorf("symlink target = %q, want it untouched", data)
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		t.Errorf("key file = %v, %v; want a regular file", info, err)
	}
	checkPrivateKeyFile(t, path)
}

func TestWriteRefusesWhatItCannotSaveSafely(t *testing.T) {
	for name, tc := range map[string]struct{ home, key string }{
		"no home":       {"", key},
		"shell quoting": {t.TempDir(), `AVNS_x"; rm -rf ~`},
		"newline":       {t.TempDir(), "AVNS_x\nAVNS_y"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := creds.Write(tc.home, other, tc.key, fsutil.WriteAtomic); err == nil {
				t.Fatal("Write succeeded")
			}
			if tc.home != "" {
				if _, err := os.Stat(creds.Path(tc.home, other)); !os.IsNotExist(err) {
					t.Errorf("key file written: %v", err)
				}
			}
		})
	}
}

func TestLoadReadsOnlyTheGatewaysKeyFile(t *testing.T) {
	for name, tc := range map[string]struct {
		file *string
		want string
	}{
		"key file":                  {ptr(key + "\n"), key},
		"surrounding space trimmed": {ptr(" " + key + "\r\n"), key},
		"invalid file":              {ptr("export AIVEN_AI_GATEWAY_KEY=x\n"), ""},
		"no file":                   {nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if tc.file != nil {
				if err := os.MkdirAll(filepath.Dir(creds.Path(home, other)), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(creds.Path(home, other), []byte(*tc.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := creds.Load(home, other); err != nil || got != tc.want {
				t.Errorf("Load = %q, %v; want %q", got, err, tc.want)
			}
			if got, err := creds.Load(home, config.DefaultBaseURL); err != nil || got != "" {
				t.Errorf("Load for another gateway = %q, %v; want none", got, err)
			}
		})
	}
	if _, err := creds.Load("", other); !errors.Is(err, config.ErrNoHome) {
		t.Errorf("Load without a home: %v", err)
	}
}

func TestFromEnvTakesTheLastValidValue(t *testing.T) {
	for name, tc := range map[string]struct {
		environ []string
		want    string
	}{
		"last value":    {[]string{"AIVEN_AI_GATEWAY_KEY=sk-old", "AIVEN_AI_GATEWAY_KEY=sk-env"}, "sk-env"},
		"invalid value": {[]string{"AIVEN_AI_GATEWAY_KEY=sk env"}, ""},
		"other name":    {[]string{"AIVEN_AI_GATEWAY_KEYS=sk-env"}, ""},
	} {
		if got := creds.FromEnv(tc.environ); got != tc.want {
			t.Errorf("%s: FromEnv = %q, want %q", name, got, tc.want)
		}
	}
}

func TestLoadReportsAnUnreadableKeyFile(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(creds.Path(home, other), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := creds.Load(home, other); err == nil || got != "" {
		t.Errorf("Load = %q, %v; want an error", got, err)
	}
}

func TestDeleteRemovesOnlyTheGatewaysKeyFile(t *testing.T) {
	home := t.TempDir()
	if err := creds.Delete(home, other, os.Remove); err != nil {
		t.Fatalf("absent file: %v", err)
	}
	for _, origin := range []string{config.DefaultBaseURL, other} {
		if _, err := creds.Write(home, origin, key, fsutil.WriteAtomic); err != nil {
			t.Fatal(err)
		}
	}
	if err := creds.Delete(home, other, os.Remove); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(creds.Path(home, other)); !os.IsNotExist(err) {
		t.Errorf("key file still there: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(creds.Path(home, other))); err != nil {
		t.Errorf("directory removed: %v", err)
	}
	if _, err := os.Stat(creds.Path(home, config.DefaultBaseURL)); err != nil {
		t.Errorf("default gateway's key file: %v", err)
	}
}

// The Aiven Console's helper falls back to AIVEN_AI_GATEWAY_KEY, which names no gateway; the launcher's reads only the
// key file of the gateway a tool was set up for, so it never prints another gateway's key.
func TestHelperReadsOnlyItsOwnGatewaysKeyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs the POSIX forms through sh")
	}
	if got := creds.Helper(config.DefaultBaseURL, "linux"); got != "cat ~/.config/aiven/ai-gateway-key" {
		t.Errorf("Helper = %q", got)
	}
	for _, goos := range []string{runtime.GOOS, "windows"} {
		t.Run(goos, func(t *testing.T) {
			home := t.TempDir()
			run := func(line string) (string, error) {
				t.Helper()
				cmd := exec.Command("/bin/sh", "-c", line)
				cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "AIVEN_AI_GATEWAY_KEY=sk-env"}
				out, err := cmd.Output()
				return string(out), err
			}
			line := "sh -c '" + creds.Helper(other, goos) + "'"
			if goos != "windows" {
				if got := creds.CommandLine(other, goos); got != line {
					t.Errorf("CommandLine = %q, want %q", got, line)
				}
			}
			if _, err := creds.Write(home, config.DefaultBaseURL, key, fsutil.WriteAtomic); err != nil {
				t.Fatal(err)
			}
			if got, err := run(line); err == nil || got != "" {
				t.Errorf("with only the default gateway's key file: %q, %v; want a failure and no key", got, err)
			}
			if _, err := creds.Write(home, other, "sk-other", fsutil.WriteAtomic); err != nil {
				t.Fatal(err)
			}
			if got, err := run(line); err != nil || got != "sk-other\n" {
				t.Errorf("with its own key file: %q, %v; want sk-other", got, err)
			}
		})
	}
}

func TestPowerShellHelperHasNothingAShellOrQuotingChanges(t *testing.T) {
	for _, origin := range []string{config.DefaultBaseURL, other, "http://[::1]:8080"} {
		script := creds.PowerShell(origin)
		if i := strings.IndexAny(script, "$\"`\\!%"); i >= 0 {
			t.Errorf("PowerShell(%q) has %q: %s", origin, script[i], script)
		}
		if !strings.Contains(script, "'"+creds.Rel(origin)+"'") || strings.Contains(script, creds.EnvVar) || !strings.HasSuffix(script, "else { [Environment]::Exit(1) }") {
			t.Errorf("PowerShell(%q) = %s", origin, script)
		}
		name, args := creds.Command(origin, "windows")
		if name != "powershell.exe" || strings.Join(args, " ") != "-NoProfile -NonInteractive -Command "+script {
			t.Errorf("Command = %q %q", name, args)
		}
		if got, want := creds.CommandLine(origin, "windows"), `powershell.exe -NoProfile -NonInteractive -Command "`+script+`"`; got != want {
			t.Errorf("CommandLine = %q, want %q", got, want)
		}
	}
}

func ptr(s string) *string { return &s }
