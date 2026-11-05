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

// configureTool previews the complete write set, including any explicitly selected default, obtains consent and
// writes it, recording the outcome in out.
func configureTool(deps Deps, p presenter, yes bool, out *targetOutcome, files []harness.File, table []models.Model) {
	tool, names := out.tool, backupNames{deps.now(), map[string]string{}}
	u := p.ui
	u.heading(toolLabel(tool) + " configuration")
	write, preview, err := changes(files, nil, table, names)
	if err != nil {
		out.status, out.reason = statusFailed, err.Error()
		_ = fail(deps, tool+": "+err.Error())
		return
	}
	if len(write) == 0 {
		u.line(2, "Already configured; no file needs a change.")
		out.status = statusUnchanged
		return
	}
	for _, f := range write {
		_, _, existed, _ := inspectFile(f.Path)
		if !existed {
			u.line(2, "Create "+f.Path)
		} else if b := names.picked[f.Path]; b != "" {
			u.line(2, "Update "+f.Path)
			u.line(4, "previous version kept as "+b)
		} else {
			u.line(2, "Update "+f.Path)
		}
	}
	if setupTools[tool].bin != "" {
		if out.asDefault {
			u.line(2, "Native default: Aiven becomes the default for "+toolLabel(tool))
		} else {
			u.line(2, "Native default: left as it is")
		}
	}
	u.line(2, "The preview is compact and redacted: generated model lists are summarised")
	u.line(2, "and credentials are hidden. The full diff is redacted the same way.")
	u.printf("\n")
	u.block(preview)
	ok, err := approveFiles(deps, p, yes, files, table, names, tool)
	switch {
	case errors.Is(err, errCancelled), errors.Is(err, errInputEnded):
		out.status, out.reason = statusCancelled, "no files written"
		_ = fail(deps, tool+": "+err.Error())
		return
	case err != nil:
		out.status, out.reason = statusFailed, err.Error()
		_ = fail(deps, tool+": "+err.Error())
		return
	case !ok && deps.Prompter == nil:
		out.status, out.reason = statusSkipped, "without a terminal, rerun with --yes to apply"
		return
	case !ok:
		out.status, out.reason = statusDeclined, "no files written"
		return
	}
	completed, err := writeFiles(write, names.name, deps)
	out.written = completed
	if err != nil {
		out.status, out.reason = statusFailed, err.Error()
		if len(completed) > 0 {
			out.status = statusPartial
		}
		_ = fail(deps, tool+": "+partialWrite(err, completed).Error())
		return
	}
	out.status = statusConfigured
}

func prepareSnapshot(env config.Env, table []models.Model, tool string, asDefault bool) (harness.Plan, error) {
	template, err := setupTools[tool].prepare(env, nil, asDefault)
	if err != nil {
		return harness.Plan{}, err
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
				return harness.Plan{}, fmt.Errorf("%s is not a regular directory; fix or move it, then retry", parent)
			}
		}
		f.Before, f.Perm, f.Existed, err = inspectFile(f.Path)
		if err != nil {
			return harness.Plan{}, err
		}
		f.Inspected = true
	}
	return applySnapshot(env, table, tool, asDefault, template)
}

func applySnapshot(env config.Env, table []models.Model, tool string, asDefault bool, snapshot harness.Plan) (harness.Plan, error) {
	for _, f := range snapshot.Files {
		if err := unchanged(f); err != nil {
			return harness.Plan{}, err
		}
	}
	plan, err := setupTools[tool].prepare(env, table, asDefault)
	if err != nil {
		return harness.Plan{}, err
	}
	if len(plan.Files) != len(snapshot.Files) {
		return harness.Plan{}, errors.New("setup write set changed during inspection")
	}
	for i := range plan.Files {
		for _, f := range snapshot.Files {
			if f.Path == plan.Files[i].Path {
				plan.Files[i].Before, plan.Files[i].Existed, plan.Files[i].Inspected = f.Before, f.Existed, f.Inspected
				plan.Files[i].Parents, plan.Files[i].Perm = f.Parents, f.Perm
			}
		}
		if !plan.Files[i].Inspected {
			return harness.Plan{}, errors.New("setup write set changed during inspection")
		}
		if err := unchanged(plan.Files[i]); err != nil {
			return harness.Plan{}, err
		}
	}
	return plan, nil
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
		if info, err := os.Lstat(parent); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf("%s is not a regular directory; nothing further was overwritten, rerun setup", parent)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect %s: %w", parent, err)
		}
	}
	data, perm, existed, err := inspectFile(f.Path)
	if err != nil {
		return err
	}
	if !f.Inspected || existed != f.Existed || perm != f.Perm || !bytes.Equal(data, f.Before) {
		return fmt.Errorf("%s changed since inspection; nothing further was overwritten, rerun setup", f.Path)
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
			return fmt.Errorf("%s changed since inspection; nothing further was overwritten, rerun setup", f.Path)
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
	return fmt.Errorf("partially configured; completed %s; remaining operation failed: %w", strings.Join(completed, ", "), err)
}

// changes lists the files that differ from what is there now, or from over, the files already planned, with their
// preview.
func changes(files, over []harness.File, table []models.Model, names backupNames) ([]harness.File, string, error) {
	var preview strings.Builder
	var out []harness.File
	for _, f := range files {
		old, _, found, err := inspectFile(f.Path)
		if err != nil {
			return nil, "", err
		}
		if f.Inspected {
			if err := unchanged(f); err != nil {
				return nil, "", err
			}
		}
		base := old
		for _, o := range over {
			if o.Path == f.Path {
				base = o.Data
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

// approveFiles obtains consent for the previewed change. --yes approves; without a terminal nothing is approved. Only
// an explicit choice or Enter applies: end of input and cancellation are errors, never a default.
func approveFiles(deps Deps, p presenter, yes bool, files []harness.File, table []models.Model, names backupNames, tool string) (bool, error) {
	if yes || deps.Prompter == nil {
		return yes, nil
	}
	full := func() error {
		all := append([]harness.File(nil), files...)
		for i := range all {
			all[i].Models = false
		}
		_, preview, err := changes(all, nil, table, names)
		if err != nil {
			return err
		}
		p.ui.printf("\n")
		p.ui.block(preview)
		return nil
	}
	if p.inline != nil {
		c := choice{title: "Apply these changes to " + toolLabel(tool) + "?", options: []option{
			{label: "Apply", detail: "write the files shown"},
			{label: "Decline", detail: "leave " + toolLabel(tool) + " as it is"},
			{label: "Show the full redacted diff", detail: "then ask again"},
		}}
		for range 10 {
			picked, err := p.inline.Select(c)
			if err != nil {
				return false, err
			}
			switch picked[0] {
			case 0:
				p.ui.line(2, "Apply: writing the changes.")
				return true, nil
			case 1:
				p.ui.line(2, "Declined: no files written for "+toolLabel(tool)+".")
				return false, nil
			}
			if err := full(); err != nil {
				return false, err
			}
		}
		return false, errors.New("no answer: expected apply or decline")
	}
	p.ui.line(2, "Enter or y applies; n declines this tool; v shows the full redacted diff")
	p.ui.line(2, "and asks again.")
	for range 10 {
		answer, err := deps.Prompter.ReadLine(fmt.Sprintf("Apply this change to %s? [Y/n/v] ", tool))
		if err != nil {
			return false, errInputEnded
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		case "v":
			if err := full(); err != nil {
				return false, err
			}
		}
	}
	return false, errors.New("no answer: expected y, n or v")
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
	return fmt.Sprintf("%d models: %s, … +%d more", len(names), strings.Join(names[:n], ", "), len(names)-n)
}
