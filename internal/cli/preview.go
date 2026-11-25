// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aymanbagabas/go-udiff"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

type approvedWrite struct {
	target      *setupTarget
	write       []harness.File
	names       backupNames
	prefAlias   string
	prefChanged bool
}

type pendingPreview struct {
	target      *setupTarget
	files       []harness.File
	write       []harness.File
	names       backupNames
	prefAlias   string
	prefChanged bool
}

type reviewOutcome int

const (
	reviewApproved reviewOutcome = iota
	reviewBack
	reviewCancelled
)

// prepareSnapshot captures a full read-only baseline of BOTH dedicated and native-default files for tool before Beat 1
// so toggling direct invocation in Beat 3 selects a subset without ever failing with a mismatched write set. When the
// native default cannot be prepared, the baseline covers the dedicated files only and nativeErr says why, so the
// default prompt can withhold that choice.
func prepareSnapshot(env config.Env, tool string) (plan harness.Plan, nativeErr, err error) {
	template, err := setupTools[tool].prepare(env, nil, true, harness.ModelSelection{})
	if err != nil {
		nativeErr = err
		template, err = setupTools[tool].prepare(env, nil, false, harness.ModelSelection{})
	}
	if err != nil {
		return harness.Plan{}, nil, err
	}
	home, configHome := filepath.Clean(env.UserHome), filepath.Clean(env.ConfigHome)
	for i := range template.Files {
		f := &template.Files[i]
		atConfigHome := false
		for parent := filepath.Dir(f.Path); parent != home &&
			parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
			if env.ConfigHome != "" && parent == configHome {
				atConfigHome = true
			}
			info, err := os.Lstat(parent)
			if atConfigHome && err == nil && info.IsDir() {
				break
			}
			f.Parents = append(f.Parents, parent)
			if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
				return harness.Plan{}, nil, fmt.Errorf(
					"%s is not a regular directory; fix or move it, then retry",
					parent,
				)
			}
		}
		f.Before, f.Perm, f.Existed, err = inspectFile(f.Path)
		if err != nil {
			return harness.Plan{}, nil, err
		}
		f.Inspected = true
	}
	return template, nativeErr, nil
}

// applySnapshot computes the write plan for (asDefault, sel) and attaches the inspected baselines from the full
// read-only snapshot. Because snapshot covers asDefault=true, every planned file is guaranteed to have a baseline.
func applySnapshot(
	env config.Env,
	table []models.Model,
	tool string,
	asDefault bool,
	snapshot harness.Plan,
	sel harness.ModelSelection,
) (harness.Plan, error) {
	plan, err := setupTools[tool].prepare(env, table, asDefault, sel)
	if err != nil {
		return harness.Plan{}, err
	}
	byPath := make(map[string]harness.File, len(snapshot.Files))
	for _, f := range snapshot.Files {
		byPath[f.Path] = f
	}
	for i := range plan.Files {
		f, ok := byPath[plan.Files[i].Path]
		if !ok || !f.Inspected {
			return harness.Plan{}, errors.New("setup write set changed during inspection")
		}
		pf := &plan.Files[i]
		pf.Before, pf.Existed, pf.Inspected = f.Before, f.Existed, f.Inspected
		pf.Parents, pf.Perm = f.Parents, f.Perm
		if err := unchanged(*pf); err != nil {
			return harness.Plan{}, err
		}
	}
	return plan, nil
}

// reviewAndApprove prints what each target will get and obtains consent, or returns reviewBack so the caller can
// step back to Beat 3 with nothing written.
func reviewAndApprove(
	deps Deps,
	p presenter,
	env config.Env,
	yes bool,
	allowBack bool,
	targets []*setupTarget,
	table []models.Model,
) ([]approvedWrite, reviewOutcome) {
	pending := pendingChanges(deps, p, targets, table)
	if len(pending) == 0 {
		return nil, reviewApproved
	}
	for _, item := range pending {
		printSummary(p.ui, item, env, table)
	}
	if !yes && deps.Prompter != nil {
		return approveInteractive(deps, p, pending, allowBack)
	}
	if yes {
		return approvedFrom(pending), reviewApproved
	}
	for _, item := range pending {
		item.target.out.status = statusSkipped
		item.target.out.reason = "without a terminal, rerun with --yes to apply"
	}
	return nil, reviewApproved
}

// pendingChanges plans each target's writes and launcher preference; a target with neither is up to date.
func pendingChanges(
	deps Deps,
	p presenter,
	targets []*setupTarget,
	table []models.Model,
) []pendingPreview {
	var pending []pendingPreview
	for _, t := range targets {
		names := backupNames{deps.now(), map[string]string{}}
		write, _, err := changes(t.plan.Files, table, names)
		prefAlias, prefChanged := "", false
		if err == nil {
			prefAlias, prefChanged, err = launcherPrefUpdate(
				t.out.tool,
				table,
				t.out.selection,
				t.currentPref,
			)
		}
		switch {
		case err != nil:
			p.ui.heading(toolLabel(t.out.tool) + " configuration")
			t.out.status, t.out.reason = statusFailed, err.Error()
			_ = fail(deps, t.out.tool+": "+err.Error())
		case len(write) == 0 && !prefChanged:
			p.ui.heading(toolLabel(t.out.tool) + " configuration")
			p.ui.line(2, "Already configured; no file needs a change.")
			t.out.status = statusUnchanged
		default:
			pending = append(pending, pendingPreview{
				target: t, files: t.plan.Files, write: write, names: names,
				prefAlias: prefAlias, prefChanged: prefChanged,
			})
		}
	}
	return pending
}

func launcherPrefUpdate(
	tool string,
	table []models.Model,
	sel harness.ModelSelection,
	currentPref string,
) (alias string, changed bool, err error) {
	if !isLauncherPrefTool(tool) || sel.IsZero() {
		return "", false, nil
	}
	chosen, err := sel.ResolveMain(table)
	if err != nil || chosen == "" {
		return "", false, err
	}
	return chosen, chosen != currentPref, nil
}

func writeApproved(deps Deps, item approvedWrite) {
	if len(item.write) == 0 {
		item.target.out.status = statusConfigured
		return
	}
	completed, err := writeFiles(item.write, item.names.name, deps)
	item.target.out.written = completed
	if err != nil {
		item.target.out.status, item.target.out.reason = statusFailed, err.Error()
		if len(completed) > 0 {
			item.target.out.status = statusPartial
		}
		_ = fail(deps, item.target.out.tool+": "+partialWrite(err, completed).Error())
		return
	}
	item.target.out.status = statusConfigured
}

// inspectFile returns path's content and permission bits, and whether it exists.
func inspectFile(path string) ([]byte, os.FileMode, bool, error) {
	data, perm, err := fsutil.ReadRegular(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	return data, perm, true, nil
}

func unchanged(f harness.File) error {
	for _, parent := range f.Parents {
		info, err := os.Lstat(parent)
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf(
				"%s is not a regular directory; nothing further was overwritten, rerun setup",
				parent,
			)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect %s: %w", parent, err)
		}
	}
	data, perm, existed, err := inspectFile(f.Path)
	if err != nil {
		return err
	}
	if !f.Inspected || existed != f.Existed || perm != f.Perm || !bytes.Equal(data, f.Before) {
		return fmt.Errorf(
			"%s changed since inspection; nothing further was overwritten, rerun setup",
			f.Path,
		)
	}
	return nil
}

// writeFiles writes each file after checking it still matches its inspection, and returns the files it completed,
// with their recovery notes, even when a later one fails: there is no transaction across files.
func writeFiles(files []harness.File, backupPath func(string) string, deps Deps) ([]string, error) {
	for _, f := range files {
		if err := unchanged(f); err != nil {
			return nil, err
		}
	}
	var completed []string
	for _, f := range files {
		note, err := writeFile(f, backupPath, deps)
		if err != nil {
			return completed, err
		}
		completed = append(completed, f.Path+note)
	}
	return completed, nil
}

// writeFile backs up and replaces f through its directory, opened once from the home down, so a parent swapped for a
// symlink after inspection cannot redirect either. The file is compared with its inspection again just before the
// rename; an edit landing after that comparison is still lost, since a rename cannot be made conditional.
func writeFile(f harness.File, backupPath func(string) string, deps Deps) (string, error) {
	if err := unchanged(f); err != nil {
		return "", err
	}
	// Parents run from the file's directory up to the home, which is not among them.
	base := filepath.Dir(f.Path)
	if len(f.Parents) > 0 {
		base = filepath.Dir(f.Parents[len(f.Parents)-1])
	}
	rel, err := filepath.Rel(base, filepath.Dir(f.Path))
	if err != nil {
		return "", err
	}
	dir, err := fsutil.OpenDir(base, rel)
	if err != nil {
		return "", fmt.Errorf("%w; nothing further was overwritten, rerun setup", err)
	}
	defer func() { _ = dir.Close() }()
	name := filepath.Base(f.Path)
	check := func() error {
		data, perm, existed, err := dir.Read(name)
		if err != nil {
			return err
		}
		if existed != f.Existed || perm != f.Perm || !bytes.Equal(data, f.Before) {
			return fmt.Errorf(
				"%s changed since inspection; nothing further was overwritten, rerun setup",
				f.Path,
			)
		}
		return nil
	}
	if err := check(); err != nil {
		return "", err
	}
	backup := ""
	if f.Backup && f.Existed {
		backup = backupPath(f.Path)
		if err := dir.Backup(name, filepath.Base(backup)); err != nil {
			return "", err
		}
	}
	// A new file is owner-only; one the user has keeps the mode they chose for it.
	perm := os.FileMode(0o600)
	if f.Existed {
		perm = f.Perm
	}
	if err := deps.writeIn(dir, name, f.Data, perm, check); err != nil {
		if backup == "" {
			return "", fmt.Errorf("replace %s: %w", f.Path, err)
		}
		return "", fmt.Errorf("replace %s (recovery copy: %s): %w", f.Path, backup, err)
	}
	return fileNote(f.Existed, backup), nil
}

func partialWrite(err error, completed []string) error {
	if len(completed) == 0 {
		return err
	}
	return fmt.Errorf(
		"partially configured; completed %s; remaining operation failed: %w",
		strings.Join(completed, ", "),
		err,
	)
}

// changes lists the files that differ from their inspected baseline (or disk when not inspected),
// with their redacted unified diff preview.
func changes(
	files []harness.File,
	table []models.Model,
	names backupNames,
) ([]harness.File, string, error) {
	var preview strings.Builder
	var out []harness.File
	for _, f := range files {
		var base []byte
		var found bool
		if f.Inspected {
			if err := unchanged(f); err != nil {
				return nil, "", err
			}
			base, found = f.Before, f.Existed
		} else {
			var err error
			base, _, found, err = inspectFile(f.Path)
			if err != nil {
				return nil, "", err
			}
		}
		if bytes.Equal(base, f.Data) {
			continue
		}
		backup := ""
		if found && f.Backup {
			backup = names.name(f.Path)
		}
		out = append(out, f)
		from, to := shown(f, base, table)
		preview.WriteString(udiff.Unified(f.Path, f.Path+fileNote(found, backup), from, to))
	}
	return out, preview.String(), nil
}

// fileNote names the recovery copy of a replaced file.
func fileNote(existed bool, backup string) string {
	switch {
	case !existed:
		return " (new file)"
	case backup != "":
		return " (previous version: " + backup + ")"
	}
	return ""
}

// modelNames lists the first n aliases in sorted order and counts the rest.
func modelNames(table []models.Model, n int) string {
	var names []string
	for _, m := range models.Sorted(table) {
		names = append(names, m.Alias)
	}
	if len(names) <= n {
		return fmt.Sprintf("%d models: %s", len(names), strings.Join(names, ", "))
	}
	return fmt.Sprintf(
		"%d models: %s, … +%d more",
		len(names),
		strings.Join(names[:n], ", "),
		len(names)-n,
	)
}
