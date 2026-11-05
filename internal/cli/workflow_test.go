// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

type callbackPrompter struct {
	answers  []string
	secret   string
	onSecret func()
	onApply  func()
}

func (p *callbackPrompter) ReadLine(prompt string) (string, error) {
	if strings.HasPrefix(prompt, "Apply") && p.onApply != nil {
		p.onApply()
		p.onApply = nil
	}
	if len(p.answers) == 0 {
		return "", errors.New("input ended")
	}
	answer := p.answers[0]
	p.answers = p.answers[1:]
	return answer, nil
}

func (p *callbackPrompter) ReadSecret(string) (string, error) {
	if p.onSecret != nil {
		p.onSecret()
		p.onSecret = nil
	}
	if p.secret != "" {
		return p.secret, nil
	}
	return "", errors.New("unexpected secret prompt")
}

func TestFullPreviewRedactsBothSidesAndAllModels(t *testing.T) {
	t.Parallel()
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	path := filepath.Join(s.fakeHome, toolFiles["pi"])
	secret := "SYNTHETIC_PRIVATE multi word password"
	writeFile(t, path, `{"providers":{"other":{"apiKey":"`+secret+`"}}}`)
	s.deps.Prompter = &scriptedPrompter{answers: []string{"", "v", ""}}
	r := s.run("setup", "pi", "--key-stdin")
	if r.code != 0 || strings.Contains(r.stdout+r.stderr, secret) {
		t.Fatalf("unsafe preview or failed setup: exit %d", r.code)
	}
	for _, model := range []string{"aiven-model", "claude-sonnet", "claude-opus", "gpt-sol", "glm"} {
		if !strings.Contains(r.stderr, `"id": "`+model+`"`) {
			t.Errorf("full view omits %s", model)
		}
	}
	if !strings.Contains(r.stderr, "<redacted ") {
		t.Fatal("full view did not redact the unrelated provider")
	}
}

func TestSetupRefusesAnEditAfterPreviewWithoutBackupOrOverwrite(t *testing.T) {
	t.Parallel()
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	path := filepath.Join(s.fakeHome, toolFiles["pi"])
	writeFile(t, path, `{"providers":{"other":{"apiKey":"SYNTHETIC_PRIVATE original"}}}`)
	edited := `{"providers":{"other":{"apiKey":"SYNTHETIC_PRIVATE concurrent"}}}`
	s.deps.Prompter = &callbackPrompter{answers: []string{"", ""}, onApply: func() { writeFile(t, path, edited) }}
	r := s.run("setup", "pi", "--key-stdin")
	if r.code != 1 || !strings.Contains(r.stderr, "changed since inspection") {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != edited || latestBackup(t, path) != "" {
		t.Fatal("concurrent bytes were overwritten or backed up as the inspected version")
	}

	s2 := newSession(t, newGatewayDouble(t))
	path2 := filepath.Join(s2.fakeHome, toolFiles["pi"])
	writeFile(t, path2, `{"providers":{"other":{"apiKey":"SYNTHETIC_PRIVATE initial"}}}`)
	s2.deps.Prompter = &callbackPrompter{
		answers:  []string{"", ""},
		secret:   keyValid,
		onSecret: func() { writeFile(t, path2, edited) },
	}
	r2 := s2.run("setup", "pi")
	if r2.code != 1 || !strings.Contains(r2.stderr, "changed since inspection") {
		t.Fatalf("edit during key prompt: exit %d: %s", r2.code, r2.stderr)
	}
	if data, err := os.ReadFile(path2); err != nil || string(data) != edited || latestBackup(t, path2) != "" {
		t.Fatal("edit during key prompt was overwritten or backed up")
	}

	if runtime.GOOS != "windows" {
		s3 := newSession(t, newGatewayDouble(t)).stdin(keyValid)
		path3 := filepath.Join(s3.fakeHome, toolFiles["pi"])
		writeFile(t, path3, `{"providers":{"other":{"apiKey":"SYNTHETIC_PRIVATE original"}}}`)
		if err := os.Chmod(path3, 0o644); err != nil {
			t.Fatal(err)
		}
		s3.deps.Prompter = &callbackPrompter{answers: []string{"", ""}, onApply: func() { _ = os.Chmod(path3, 0o600) }}
		r3 := s3.run("setup", "pi", "--key-stdin")
		if r3.code != 1 || !strings.Contains(r3.stderr, "changed since inspection") {
			t.Fatalf("mode change during apply prompt: exit %d: %s", r3.code, r3.stderr)
		}
		if info, err := os.Stat(path3); err != nil || info.Mode().Perm() != 0o600 || latestBackup(t, path3) != "" {
			t.Fatalf("mode = %v (%v), backup = %q", info, err, latestBackup(t, path3))
		}
	}
}

func TestSetupRefusesNonRegularTargetsBeforeGatewayOrKeySave(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"symlink", "directory", "parent symlink"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			g := newGatewayDouble(t)
			s := newSession(t, g).stdin(keyValid)
			path := filepath.Join(s.fakeHome, toolFiles["pi"])
			outside := filepath.Join(t.TempDir(), "outside")
			writeFile(t, outside, `{"providers":{}}`)
			if kind == "parent symlink" {
				if err := os.Symlink(filepath.Dir(outside), filepath.Join(s.fakeHome, ".pi")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				var err error
				if kind == "symlink" {
					err = os.Symlink(outside, path)
				} else {
					err = os.Mkdir(path, 0o700)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			r := s.run("setup", "pi", "--key-stdin", "--yes")
			if r.code != 1 || !strings.Contains(r.stderr, "regular") && !strings.Contains(r.stderr, "is a directory") || len(g.journal()) != 0 {
				t.Fatalf("exit %d, requests %d: %s", r.code, len(g.journal()), r.stderr)
			}
			if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
				t.Fatal("unsafe target caused a credential save")
			}
			if data, _ := os.ReadFile(outside); string(data) != `{"providers":{}}` {
				t.Fatal("symlink destination changed")
			}
		})
	}
}

func TestSetupRejectsMalformedNativeSettingsBeforeSavingAKey(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ tool, file string }{
		{"pi", ".pi/agent/settings.json"},
		{"claude", ".claude/settings.json"},
		{"claude", "../project/.claude/settings.local.json"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			g := newGatewayDouble(t)
			s := newSession(t, g).stdin(keyValid)
			path := filepath.Join(s.fakeHome, tc.file)
			writeFile(t, path, `{"password":"SYNTHETIC_PARSE_SECRET", invalid`)
			r := s.run("setup", tc.tool, "--key-stdin", "--yes")
			if r.code != 1 || !strings.Contains(r.stderr, "not a JSON object") ||
				strings.Contains(r.stdout+r.stderr, "SYNTHETIC_PARSE_SECRET") || len(g.journal()) != 0 {
				t.Fatalf("exit %d, requests %d: %s", r.code, len(g.journal()), r.stderr)
			}
			if _, err := os.Stat(s.keyPath()); !os.IsNotExist(err) {
				t.Fatal("malformed consumed settings caused a key save")
			}
		})
	}
}

func TestSetupReportsCompletedFileAndRecoveryAfterSecondFileFails(t *testing.T) {
	t.Parallel()
	s := newSession(t, newGatewayDouble(t)).stdin(keyValid)
	main := filepath.Join(s.fakeHome, ".codex", "config.toml")
	profile := filepath.Join(s.fakeHome, ".codex", "aiven.config.toml")
	writeFile(t, main, "model = 'native-model'\n")
	writeFile(t, profile, "model = 'gpt-sol'\n")
	s.deps.WriteFile = func(path string, data []byte, mode os.FileMode) error {
		if path == profile {
			return os.ErrPermission
		}
		return fsutil.WriteAtomic(path, data, mode)
	}
	r := s.run("setup", "codex", "--key-stdin", "--yes")
	if r.code != 1 || !strings.Contains(r.stderr, "partially configured; completed "+main) ||
		!strings.Contains(r.stderr, "recovery copy: "+profile+".aiven-") {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if latestBackup(t, main) != "model = 'native-model'\n" || latestBackup(t, profile) != "model = 'gpt-sol'\n" {
		t.Fatal("recovery copies do not hold the inspected bytes")
	}
	if data, _ := os.ReadFile(profile); string(data) != "model = 'gpt-sol'\n" {
		t.Fatal("failed file lost its previous version")
	}
}
