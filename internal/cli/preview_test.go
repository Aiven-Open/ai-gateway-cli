// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

var (
	previewNow   = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	previewTable = []models.Model{{Alias: "claude-sonnet-5-5"}, {Alias: "glm-5"}, {Alias: "gpt-6.1-sol"}}
)

// lineAnswers answers ReadLine from a queue, running hook before each prompt; an exhausted queue is end of input.
type lineAnswers struct {
	answers []string
	prompts []string
	hook    func()
}

func (p *lineAnswers) ReadSecret(string) (string, error) { return "", io.EOF }

func (p *lineAnswers) ReadLine(prompt string) (string, error) {
	p.prompts = append(p.prompts, prompt)
	if p.hook != nil {
		p.hook()
	}
	if len(p.answers) == 0 {
		return "", io.EOF
	}
	a := p.answers[0]
	p.answers = p.answers[1:]
	return a, nil
}

func previewEnv(t *testing.T) config.Env {
	home := t.TempDir()
	return config.Env{BaseURL: "https://gateway.example.invalid", UserHome: home}
}

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(data)
}

// configure snapshots pi's write set and runs configureTool on it, returning the outcome and stderr.
func configure(t *testing.T, env config.Env, deps Deps, yes, asDefault bool) (targetOutcome, string) {
	t.Helper()
	plan, err := prepareSnapshot(env, previewTable, "pi", asDefault)
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	deps.Stderr, deps.Now = &stderr, func() time.Time { return previewNow }
	out := targetOutcome{tool: "pi", asDefault: asDefault}
	configureTool(deps, newPresenter(env, deps, true), yes, &out, plan.Files, previewTable)
	return out, stderr.String()
}

func TestConfigureToolNamesTheBackupItKeeps(t *testing.T) {
	env := previewEnv(t)
	path := filepath.Join(env.UserHome, ".pi/agent/models.json")
	const before = `{"providers":{}}`
	writeTestFile(t, path, before)
	backup := fsutil.BackupPath(path, previewNow)
	note := " (previous version: " + backup + ")"
	out, stderr := configure(t, env, Deps{}, true, false)
	if out.status != statusConfigured || len(out.written) != 1 || out.written[0] != path+note {
		t.Fatalf("outcome %+v, stderr %q", out, stderr)
	}
	for _, want := range []string{"Update " + path + "\n", "previous version kept as " + backup + "\n", "+++ " + path + note + "\n",
		"Native default: left as it is\n", "[3 models: claude-sonnet-5-5, glm-5, … +1 more]"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %q", want, stderr)
		}
	}
	if got := readTestFile(t, backup); got != before {
		t.Errorf("backup = %q, want %q", got, before)
	}
	if got := readTestFile(t, path); !strings.Contains(got, `"glm-5"`) {
		t.Errorf("models.json = %q, want the gateway models", got)
	}
	if info, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("models.json mode: %v, %v", info, err)
	}
	if out, stderr = configure(t, env, Deps{}, true, false); out.status != statusUnchanged || !strings.Contains(stderr, "Already configured") {
		t.Errorf("rerun: outcome %+v, stderr %q", out, stderr)
	}
}

func TestConfigureToolKeepsTheModeOfAnExistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permission bits")
	}
	env := previewEnv(t)
	path := filepath.Join(env.UserHome, ".pi/agent/models.json")
	settings := filepath.Join(env.UserHome, ".pi/agent/settings.json")
	writeTestFile(t, path, `{"providers":{}}`)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if out, stderr := configure(t, env, Deps{}, true, true); out.status != statusConfigured {
		t.Fatalf("outcome %+v, stderr %q", out, stderr)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o640 {
		t.Errorf("models.json mode = %o, want 640 kept", info.Mode().Perm())
	}
	if info, err := os.Stat(settings); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Errorf("new settings.json mode = %o, want 600", info.Mode().Perm())
	}
}

func TestConfigureToolWritesXDGConfigHomeOutsideUserHome(t *testing.T) {
	env := previewEnv(t)
	env.UserHome += string(filepath.Separator)
	env.ConfigHome = filepath.Join(t.TempDir(), "xdg")
	plan, err := prepareSnapshot(env, previewTable, "opencode", false)
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	deps := Deps{Stderr: &stderr, Now: func() time.Time { return previewNow }}
	out := targetOutcome{tool: "opencode"}
	configureTool(deps, newPresenter(env, deps, true), true, &out, plan.Files, previewTable)
	if out.status != statusConfigured {
		t.Fatalf("outcome %+v, stderr %q", out, stderr.String())
	}
	if got := readTestFile(t, filepath.Join(env.ConfigHome, "opencode/opencode.json")); !strings.Contains(got, `"aiven"`) {
		t.Errorf("opencode.json = %q, want Aiven provider", got)
	}
	link := filepath.Join(t.TempDir(), "xdg-link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	env.ConfigHome = link
	if _, err := prepareSnapshot(env, previewTable, "opencode", false); err == nil || !strings.Contains(err.Error(), link+" is not a regular directory") {
		t.Errorf("snapshot through symlinked XDG_CONFIG_HOME: %v", err)
	}
}

func TestConfigureToolKeepsCommentsAndSummarisesTheModels(t *testing.T) {
	env := previewEnv(t)
	path := filepath.Join(env.UserHome, ".pi/agent/models.json")
	const before = "{\n  // local models\n  \"providers\": {\n    \"local\": {\"baseUrl\": \"http://localhost\"}, // mine\n  },\n}\n"
	writeTestFile(t, path, before)
	out, stderr := configure(t, env, Deps{}, true, false)
	if out.status != statusConfigured || !strings.Contains(stderr, "[3 models: claude-sonnet-5-5, glm-5, … +1 more]") {
		t.Fatalf("outcome %+v, stderr %q", out, stderr)
	}
	got := readTestFile(t, path)
	if !strings.HasPrefix(got, "{\n  // local models\n  \"providers\": {\n    \"local\": {\"baseUrl\": \"http://localhost\"}, // mine\n    \"aiven\": {\n") {
		t.Errorf("models.json = %q", got)
	}
}

func TestConfigureToolSummarisesTheOMPModels(t *testing.T) {
	env := previewEnv(t)
	path := filepath.Join(env.UserHome, ".omp/agent/models.yml")
	writeTestFile(t, path, "# local models\nproviders:\n  local:\n    baseUrl: http://localhost\n")
	plan, err := prepareSnapshot(env, previewTable, "omp", false)
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	deps := Deps{Stderr: &stderr, Now: func() time.Time { return previewNow }}
	out := targetOutcome{tool: "omp"}
	configureTool(deps, newPresenter(env, deps, true), true, &out, plan.Files, previewTable)
	if out.status != statusConfigured || !strings.Contains(stderr.String(), "+    models: [3 models: claude-sonnet-5-5, glm-5, … +1 more]\n") ||
		strings.Contains(stderr.String(), "gpt-6.1-sol") {
		t.Fatalf("outcome %+v, stderr %q", out, stderr.String())
	}
	if got := readTestFile(t, path); !strings.HasPrefix(got, "# local models\n") || !strings.Contains(got, "id: gpt-6.1-sol") {
		t.Errorf("models.yml = %q, want every gateway model", got)
	}
}

func TestConfigureToolWritesOnlyOnConsent(t *testing.T) {
	const ask = "Apply this change to pi? [Y/n/v] "
	for _, tc := range []struct {
		name     string
		yes      bool
		prompter *lineAnswers
		status   targetStatus
		diffs    int
	}{
		{name: "--yes", yes: true, status: statusConfigured, diffs: 1},
		{name: "enter", prompter: &lineAnswers{answers: []string{""}}, status: statusConfigured, diffs: 1},
		{name: "y", prompter: &lineAnswers{answers: []string{"y"}}, status: statusConfigured, diffs: 1},
		{name: "n", prompter: &lineAnswers{answers: []string{"n"}}, status: statusDeclined, diffs: 1},
		{name: "full diff then n", prompter: &lineAnswers{answers: []string{"v", "n"}}, status: statusDeclined, diffs: 2},
		{name: "end of input", prompter: &lineAnswers{}, status: statusCancelled, diffs: 1},
		{name: "no terminal", status: statusSkipped, diffs: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := previewEnv(t)
			path := filepath.Join(env.UserHome, ".pi/agent/models.json")
			const before = `{"providers":{"other":{"apiKey":"SYNTHETIC_user_secret"}}}`
			writeTestFile(t, path, before)
			deps := Deps{}
			if tc.prompter != nil {
				deps.Prompter = tc.prompter
			}
			out, stderr := configure(t, env, deps, tc.yes, false)
			if out.status != tc.status {
				t.Fatalf("status %d, want %d; stderr %q", out.status, tc.status, stderr)
			}
			if got := strings.Count(stderr, "+++ "+path); got != tc.diffs {
				t.Errorf("%d diffs, want %d: %q", got, tc.diffs, stderr)
			}
			if strings.Contains(stderr, "SYNTHETIC_user_secret") {
				t.Error("the preview showed the credential")
			}
			if tc.prompter != nil && (len(tc.prompter.prompts) == 0 || tc.prompter.prompts[0] != ask) {
				t.Errorf("prompts %q", tc.prompter.prompts)
			}
			written := readTestFile(t, path) != before
			if written != (tc.status == statusConfigured) {
				t.Errorf("status %d, file written %v", out.status, written)
			}
			if baks, _ := filepath.Glob(path + ".aiven-*.bak"); len(baks) != 0 && !written {
				t.Errorf("backups %v without a write", baks)
			}
		})
	}
}

func TestConfigureToolStopsWhenAFileChangesAfterInspection(t *testing.T) {
	env := previewEnv(t)
	path := filepath.Join(env.UserHome, ".pi/agent/models.json")
	writeTestFile(t, path, `{"providers":{}}`)
	plan, err := prepareSnapshot(env, previewTable, "pi", false)
	if err != nil {
		t.Fatal(err)
	}
	const edited = `{"providers":{"edited":{}}}`
	var stderr strings.Builder
	deps := Deps{Stderr: &stderr, Prompter: &lineAnswers{answers: []string{"y"}, hook: func() { writeTestFile(t, path, edited) }}}
	out := targetOutcome{tool: "pi"}
	configureTool(deps, newPresenter(env, deps, true), false, &out, plan.Files, previewTable)
	if out.status != statusFailed || !strings.Contains(out.reason, path+" changed since inspection") || len(out.written) != 0 {
		t.Fatalf("outcome %+v, stderr %q", out, stderr.String())
	}
	if got := readTestFile(t, path); got != edited {
		t.Errorf("models.json = %q, want the edit kept", got)
	}
	if baks, _ := filepath.Glob(path + ".aiven-*.bak"); len(baks) != 0 {
		t.Errorf("backups %v", baks)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		plan, err = prepareSnapshot(env, previewTable, "pi", false)
		if err != nil {
			t.Fatal(err)
		}
		stderr.Reset()
		deps.Prompter = &lineAnswers{answers: []string{"y"}, hook: func() { _ = os.Chmod(path, 0o600) }}
		out = targetOutcome{tool: "pi"}
		configureTool(deps, newPresenter(env, deps, true), false, &out, plan.Files, previewTable)
		if out.status != statusFailed || !strings.Contains(out.reason, path+" changed since inspection") || len(out.written) != 0 {
			t.Fatalf("mode change outcome %+v, stderr %q", out, stderr.String())
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("models.json mode = %v, %v; want 0600 kept", info, err)
		}
		if baks, _ := filepath.Glob(path + ".aiven-*.bak"); len(baks) != 0 {
			t.Errorf("backups %v after mode change", baks)
		}
	}
}

func TestConfigureToolChecksEveryFileBeforeTouchingAny(t *testing.T) {
	// A change to a later file stops the write before the first file, and one made while an earlier file is written
	// stops it before that file is backed up.
	for _, during := range []string{"prompt", "write"} {
		t.Run(during, func(t *testing.T) {
			env := previewEnv(t)
			modelsPath, settings := filepath.Join(env.UserHome, ".pi/agent/models.json"), filepath.Join(env.UserHome, ".pi/agent/settings.json")
			writeTestFile(t, settings, `{"theme":"dark"}`)
			plan, err := prepareSnapshot(env, previewTable, "pi", true)
			if err != nil {
				t.Fatal(err)
			}
			const edited = `{"theme":"light"}`
			edit := func() { writeTestFile(t, settings, edited) }
			var stderr strings.Builder
			deps := Deps{Stderr: &stderr, Prompter: &lineAnswers{answers: []string{"y"}}}
			want := targetOutcome{tool: "pi", asDefault: true, status: statusFailed}
			if during == "prompt" {
				deps.Prompter = &lineAnswers{answers: []string{"y"}, hook: edit}
			} else {
				deps.WriteFile = func(path string, data []byte, perm os.FileMode) error {
					if path == modelsPath {
						edit()
					}
					return fsutil.WriteAtomic(path, data, perm)
				}
				want.status, want.written = statusPartial, []string{modelsPath + " (new file)"}
			}
			out := targetOutcome{tool: "pi", asDefault: true}
			configureTool(deps, newPresenter(env, deps, true), false, &out, plan.Files, previewTable)
			if out.status != want.status || strings.Join(out.written, ",") != strings.Join(want.written, ",") ||
				!strings.Contains(out.reason, settings+" changed since inspection") {
				t.Fatalf("outcome %+v, stderr %q", out, stderr.String())
			}
			if got := readTestFile(t, settings); got != edited {
				t.Errorf("settings.json = %q, want the edit kept", got)
			}
			if baks, _ := filepath.Glob(settings + ".aiven-*.bak"); len(baks) != 0 {
				t.Errorf("backups %v of a file that changed", baks)
			}
			if _, err := os.Stat(modelsPath); (err == nil) != (during == "write") {
				t.Errorf("models.json written: %v", err == nil)
			}
		})
	}
}

func TestWriteFilesKeepsAnEditMadeWhileTheBackupIsTaken(t *testing.T) {
	env := previewEnv(t)
	path := filepath.Join(env.UserHome, ".pi/agent/models.json")
	writeTestFile(t, path, `{"providers":{}}`)
	plan, err := prepareSnapshot(env, previewTable, "pi", false)
	if err != nil {
		t.Fatal(err)
	}
	const edited = `{"providers":{"edited":{}}}`
	backup := fsutil.BackupPath(path, previewNow)
	// The name is picked after the first check, so only the check before the replace sees this edit.
	completed, err := writeFiles(plan.Files, func(string) string { writeTestFile(t, path, edited); return backup }, Deps{})
	if err == nil || !strings.Contains(err.Error(), path+" changed since inspection") || len(completed) != 0 {
		t.Fatalf("completed %v, err %v", completed, err)
	}
	if got := readTestFile(t, path); got != edited {
		t.Errorf("models.json = %q, want the edit kept", got)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		plan, err = prepareSnapshot(env, previewTable, "pi", false)
		if err != nil {
			t.Fatal(err)
		}
		backup2 := fsutil.BackupPath(path, previewNow)
		completed, err = writeFiles(plan.Files, func(string) string { _ = os.Chmod(path, 0o600); return backup2 }, Deps{})
		if err == nil || !strings.Contains(err.Error(), path+" changed since inspection") || len(completed) != 0 {
			t.Fatalf("mode change completed %v, err %v", completed, err)
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 || readTestFile(t, path) != edited {
			t.Errorf("models.json = %v (%v), want 0600 and content kept", info, err)
		}
	}
}

func TestWriteFilesRefusesASymlinkSwappedBeforeBackup(t *testing.T) {
	env := previewEnv(t)
	path := filepath.Join(env.UserHome, ".pi/agent/models.json")
	writeTestFile(t, path, `{"providers":{}}`)
	plan, err := prepareSnapshot(env, previewTable, "pi", false)
	if err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(filepath.Dir(path), "other")
	writeTestFile(t, victim, "keep")
	swap := func(string) string {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Base(victim), path); err != nil {
			t.Fatal(err)
		}
		return path + ".bak"
	}
	if _, err := writeFiles(plan.Files[:1], swap, Deps{}); err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("writeFiles through swapped symlink: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("backup created from swapped symlink: %v", err)
	}
}

func TestWriteFilesStaysInTheInspectedDirectoryWhenItsPathIsReplaced(t *testing.T) {
	env := previewEnv(t)
	agent := filepath.Join(env.UserHome, ".pi/agent")
	const before = `{"providers":{}}`
	writeTestFile(t, filepath.Join(agent, "models.json"), before)
	plan, err := prepareSnapshot(env, previewTable, "pi", false)
	if err != nil {
		t.Fatal(err)
	}
	moved, elsewhere := filepath.Join(env.UserHome, "moved"), t.TempDir()
	// The name is picked once the directory is open: from here on, the path names another directory.
	swap := func(path string) string {
		if err := os.Rename(agent, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, agent); err != nil {
			t.Fatal(err)
		}
		return fsutil.BackupPath(path, previewNow)
	}
	completed, err := writeFiles(plan.Files[:1], swap, Deps{})
	if err != nil || len(completed) != 1 {
		t.Fatalf("completed %v, err %v", completed, err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("wrote through the symlink: %v", entries)
	}
	if got := readTestFile(t, filepath.Join(moved, "models.json")); got == before || !strings.Contains(got, `"aiven"`) {
		t.Errorf("models.json = %q, want it replaced in the directory inspected", got)
	}
	if got := readTestFile(t, filepath.Join(moved, "models.json.aiven-20261028T120000Z.bak")); got != before {
		t.Errorf("backup = %q, want the inspected content", got)
	}
}

func TestConfigureToolKeepsThePreviewedBackupNameWhenAnotherRunTakesIt(t *testing.T) {
	env := previewEnv(t)
	path := filepath.Join(env.UserHome, ".pi/agent/models.json")
	const before = `{"providers":{}}`
	writeTestFile(t, path, before)
	taken := fsutil.BackupPath(path, previewNow)
	plan, err := prepareSnapshot(env, previewTable, "pi", false)
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	deps := Deps{Stderr: &stderr, Now: func() time.Time { return previewNow },
		Prompter: &lineAnswers{answers: []string{"y"}, hook: func() { writeTestFile(t, taken, "another run") }}}
	out := targetOutcome{tool: "pi"}
	configureTool(deps, newPresenter(env, deps, true), false, &out, plan.Files, previewTable)
	if out.status != statusFailed || !strings.Contains(stderr.String(), "(previous version: "+taken+")") ||
		!strings.Contains(out.reason, taken+" already exists; retry") {
		t.Fatalf("outcome %+v, stderr %q", out, stderr.String())
	}
	if got := readTestFile(t, path); got != before {
		t.Errorf("models.json = %q, want it untouched", got)
	}
}

func TestConfigureToolReportsAPartialWrite(t *testing.T) {
	env := previewEnv(t)
	modelsPath, settings := filepath.Join(env.UserHome, ".pi/agent/models.json"), filepath.Join(env.UserHome, ".pi/agent/settings.json")
	const before = `{"theme":"dark"}`
	writeTestFile(t, settings, before)
	deps := Deps{WriteFile: func(path string, data []byte, perm os.FileMode) error {
		if path == settings {
			return errors.New("disk full")
		}
		return fsutil.WriteAtomic(path, data, perm)
	}}
	out, stderr := configure(t, env, deps, true, true)
	if out.status != statusPartial || len(out.written) != 1 || out.written[0] != modelsPath+" (new file)" {
		t.Fatalf("outcome %+v, stderr %q", out, stderr)
	}
	if !strings.Contains(stderr, "partially configured; completed "+modelsPath+" (new file); remaining operation failed: replace "+settings) ||
		!strings.Contains(stderr, "Native default: Aiven becomes the default for Pi") {
		t.Errorf("stderr %q", stderr)
	}
	if got := readTestFile(t, settings); got != before {
		t.Errorf("settings.json = %q, want it untouched", got)
	}
}

func TestConfigureToolNamesNoRecoveryCopyForANewFile(t *testing.T) {
	env := previewEnv(t)
	modelsPath := filepath.Join(env.UserHome, ".pi/agent/models.json")
	deps := Deps{WriteFile: func(string, []byte, os.FileMode) error { return errors.New("disk full") }}
	out, stderr := configure(t, env, deps, true, false)
	if out.status != statusFailed || !strings.Contains(stderr, "replace "+modelsPath+": disk full") ||
		strings.Contains(stderr, "recovery copy") {
		t.Errorf("outcome %+v, stderr %q", out, stderr)
	}
}

func TestPrepareSnapshotRefusesRedirectedParents(t *testing.T) {
	env := previewEnv(t)
	plan, err := prepareSnapshot(env, previewTable, "pi", false)
	if err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(env.UserHome, ".pi/agent")
	if f := plan.Files[0]; !f.Inspected || f.Existed || strings.Join(f.Parents, ",") != agent+","+filepath.Dir(agent) {
		t.Fatalf("snapshot %+v", f)
	}
	elsewhere := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(agent), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, agent); err != nil {
		t.Fatal(err)
	}
	// The directory moved after inspection: nothing is written through the link.
	var stderr strings.Builder
	deps := Deps{Stderr: &stderr}
	out := targetOutcome{tool: "pi"}
	configureTool(deps, newPresenter(env, deps, true), true, &out, plan.Files, previewTable)
	if out.status != statusFailed || !strings.Contains(out.reason, agent+" is not a regular directory") {
		t.Errorf("outcome %+v, stderr %q", out, stderr.String())
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("wrote through the link: %v", entries)
	}
	if _, err := prepareSnapshot(env, previewTable, "pi", false); err == nil || !strings.Contains(err.Error(), agent+" is not a regular directory") {
		t.Errorf("snapshot through a link: %v", err)
	}
}

func TestPrepareSnapshotRefusesRedirectedOMPHome(t *testing.T) {
	for _, when := range []string{"before inspection", "after inspection"} {
		t.Run(when, func(t *testing.T) {
			t.Parallel()
			env := previewEnv(t)
			ompHome := filepath.Join(env.UserHome, ".omp")
			elsewhere := t.TempDir()
			if err := os.MkdirAll(filepath.Join(elsewhere, "agent"), 0o700); err != nil {
				t.Fatal(err)
			}
			redirect := func() {
				t.Helper()
				if err := os.Symlink(elsewhere, ompHome); err != nil {
					t.Fatal(err)
				}
			}
			if when == "before inspection" {
				redirect()
			}
			plan, err := prepareSnapshot(env, previewTable, "omp", true)
			if when == "before inspection" {
				if err == nil || !strings.Contains(err.Error(), ompHome+" is not a regular directory") {
					t.Fatalf("snapshot through redirected OMP home: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			redirect()
			var stderr strings.Builder
			deps := Deps{Stderr: &stderr}
			out := targetOutcome{tool: "omp", asDefault: true}
			configureTool(deps, newPresenter(env, deps, true), true, &out, plan.Files, previewTable)
			if out.status != statusFailed || !strings.Contains(out.reason, ompHome+" is not a regular directory") {
				t.Errorf("outcome %+v, stderr %q", out, stderr.String())
			}
			if entries, err := os.ReadDir(filepath.Join(elsewhere, "agent")); err != nil || len(entries) != 0 {
				t.Errorf("files written through redirected OMP home: %v, err %v", entries, err)
			}
		})
	}
}

func TestYAMLPreviewRedactsCredentialsOnBothSides(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		secrets    []string
	}{
		{"plain", "    apiKey: SYNTHETIC_plain value\n", []string{"SYNTHETIC_plain value"}},
		{"single quoted", "    password: 'SYNTHETIC_quoted value'\n", []string{"SYNTHETIC_quoted value"}},
		{"literal", "    apiKey: |-\n      SYNTHETIC_first\n      SYNTHETIC_second\n", []string{"SYNTHETIC_first", "SYNTHETIC_second"}},
		{"folded", "    secret: >-\n      SYNTHETIC_folded\n      SYNTHETIC_next\n", []string{"SYNTHETIC_folded", "SYNTHETIC_next"}},
		{"flow", "    auth: {password: SYNTHETIC_flow, token: SYNTHETIC_token}\n", []string{"SYNTHETIC_flow", "SYNTHETIC_token"}},
		{"untrusted reference", "    apiKey: OPENAI_API_KEY\n", []string{"OPENAI_API_KEY"}},
		{"aliased credential", "    defaultValue: &credential SYNTHETIC_aliased value\n    apiKey: *credential\n", []string{"SYNTHETIC_aliased value"}},
		{"anchor in sequence", "    values:\n      - &credential SYNTHETIC_sequence_secret\n    apiKey: *credential\n", []string{"SYNTHETIC_sequence_secret"}},
		{"numeric sequence", "    values: [&credential 76543210]\n    apiKey: *credential\n", []string{"76543210"}},
		{"tagged", "    apiKey: !!str SYNTHETIC_tagged\n", []string{"SYNTHETIC_tagged"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := "providers:\n  unrelated:\n" + tc.body + "    baseUrl: https://example.invalid/v1\n"
			after := before + "  aiven:\n    apiKey: " + creds.EnvVar + "\n"
			f := harness.File{Path: "/home/ada/.omp/agent/models.yml", Data: []byte(after)}
			from, to := shown(f, []byte(before), nil)
			for _, secret := range tc.secrets {
				if strings.Contains(from+to, secret) {
					t.Errorf("credential shown: %q / %q", from, to)
				}
			}
			if !strings.Contains(to, "apiKey: "+creds.EnvVar) || !strings.Contains(from, "baseUrl: https://example.invalid/v1") {
				t.Errorf("redacted too much: %q / %q", from, to)
			}
		})
	}
	if from, _ := shown(harness.File{Path: "/x/models.yml"}, []byte("a: [b\n"), nil); from != "<redacted: YAML preview unavailable>\n" {
		t.Errorf("unparsable YAML shown as %q", from)
	}
	later := "theme: dark\n---\ndefaultValue: &credential SYNTHETIC_later_document\napiKey: *credential\n"
	if from, to := shown(harness.File{Path: "/x/models.yml", Data: []byte(later + "x: 1\n")}, []byte(later), nil); strings.Contains(from+to, "SYNTHETIC") || !strings.Contains(from, "theme: dark") {
		t.Errorf("aliased credential in a later document: %q / %q", from, to)
	}
}
