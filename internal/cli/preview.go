// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aymanbagabas/go-udiff"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

type approvedWrite struct {
	target    *setupTarget
	write     []harness.File
	names     backupNames
	prefAlias string
}

type pendingPreview struct {
	target      *setupTarget
	files       []harness.File
	write       []harness.File
	preview     string
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
// so toggling direct invocation in Beat 3 selects a subset without ever failing with a mismatched write set.
func prepareSnapshot(env config.Env, tool string) (harness.Plan, error) {
	template, err := setupTools[tool].prepare(env, nil, true, harness.ModelSelection{})
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
				return harness.Plan{}, fmt.Errorf(
					"%s is not a regular directory; fix or move it, then retry",
					parent,
				)
			}
		}
		f.Before, f.Perm, f.Existed, err = inspectFile(f.Path)
		if err != nil {
			return harness.Plan{}, err
		}
		f.Inspected = true
	}
	return template, nil
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

// reviewAndApprove renders the rail-free Review table/diffs and obtains user consent (or returns reviewBack so the
// caller can step backward to Beat 3 with zero disk writes).
func reviewAndApprove(
	deps Deps,
	p presenter,
	env config.Env,
	yes bool,
	allowBack bool,
	targets []*setupTarget,
	table []models.Model,
) ([]approvedWrite, reviewOutcome) {
	var pending []pendingPreview
	for _, t := range targets {
		names := backupNames{deps.now(), map[string]string{}}
		write, preview, err := changes(t.plan.Files, nil, table, names)
		if err != nil {
			p.ui.heading(toolLabel(t.out.tool) + " configuration")
			t.out.status, t.out.reason = statusFailed, err.Error()
			_ = fail(deps, t.out.tool+": "+err.Error())
			continue
		}
		prefAlias, prefChanged := launcherPrefUpdate(t.out.tool, env, table, t.out.selection)
		if len(write) == 0 && !prefChanged {
			p.ui.heading(toolLabel(t.out.tool) + " configuration")
			p.ui.line(2, "Already configured; no file needs a change.")
			t.out.status = statusUnchanged
			continue
		}
		pending = append(pending, pendingPreview{
			target:      t,
			files:       t.plan.Files,
			write:       write,
			preview:     preview,
			names:       names,
			prefAlias:   prefAlias,
			prefChanged: prefChanged,
		})
	}
	if len(pending) == 0 {
		return nil, reviewApproved
	}

	// Non-interactive or --yes: print each diff and apply (--yes) or skip (no terminal).
	if yes || deps.Prompter == nil {
		var approved []approvedWrite
		for _, item := range pending {
			printSinglePreviewHeader(p.ui, item, env)
			if yes {
				approved = append(approved, approvedWrite{
					target:    item.target,
					write:     item.write,
					names:     item.names,
					prefAlias: item.prefAlias,
				})
			} else {
				item.target.out.status = statusSkipped
				item.target.out.reason = "without a terminal, rerun with --yes to apply"
			}
		}
		return approved, reviewApproved
	}

	// Single-tool interactive review: rail-free diff + Apply / Show full diff / Back / Cancel.
	if len(pending) == 1 {
		item := pending[0]
		printSinglePreviewHeader(p.ui, item, env)
		approved, outcome := approveSingleInteractive(deps, p, item, table, allowBack)
		return approved, outcome
	}

	// Multi-tool interactive review: compact rail-free table + Apply all / Inspect redacted diffs / Back / Cancel.
	printReviewTable(p.ui, env, pending, table)
	for {
		action, err := promptMultiReview(deps, p, len(pending), allowBack)
		switch {
		case errors.Is(err, errBack):
			return nil, reviewBack
		case err != nil, action == multiActionCancel:
			if err != nil {
				_ = fail(deps, err.Error())
			}
			for _, item := range pending {
				item.target.out.status, item.target.out.reason = statusCancelled, "no files written"
			}
			return nil, reviewCancelled
		case action == multiActionBack:
			return nil, reviewBack
		case action == multiActionInspect:
			for i, item := range pending {
				p.ui.heading(fmt.Sprintf("[%d/%d] %s", i+1, len(pending), toolLabel(item.target.out.tool)))
				printFileSummary(p.ui, 2, item, env)
				if item.preview != "" {
					p.ui.printf("\n")
					p.ui.block(item.preview)
				}
			}
			continue
		case action == multiActionApply:
			approved := make([]approvedWrite, 0, len(pending))
			for _, item := range pending {
				approved = append(approved, approvedWrite{
					target:    item.target,
					write:     item.write,
					names:     item.names,
					prefAlias: item.prefAlias,
				})
			}
			return approved, reviewApproved
		}
	}
}

func launcherPrefUpdate(
	tool string,
	env config.Env,
	table []models.Model,
	sel harness.ModelSelection,
) (alias string, changed bool) {
	if tool != "pi" && tool != "opencode" && tool != "omp" {
		return "", false
	}
	if sel.IsZero() {
		return "", false
	}
	chosen, err := sel.ResolveMain(table)
	if err != nil || chosen == "" {
		return "", false
	}
	current, _ := config.LoadModelPref(env.UserHome, env.BaseURL, tool)
	effectiveCurrent := cmp.Or(current, models.SonnetOrFirst(table))
	return chosen, chosen != effectiveCurrent
}

func printReviewTable(u ui, env config.Env, pending []pendingPreview, table []models.Model) {
	rows := make([][]string, 0, len(pending))
	for _, item := range pending {
		t := item.target
		model := resolvedToolModel(t.out.tool, env, table, t.plan, t.out.selection)
		direct := "no (launch only)"
		if t.out.asDefault {
			direct = "yes"
		}
		var files []string
		for _, f := range item.write {
			files = append(files, shortHomePath(env.UserHome, f.Path))
		}
		if item.prefChanged {
			files = append(files, shortHomePath(env.UserHome, config.ModelPrefsPath(env.UserHome)))
		}
		rows = append(rows, []string{
			toolLabel(t.out.tool),
			safe(model),
			direct,
			strings.Join(files, ", "),
		})
	}
	u.printf("\n")
	u.table(2, []string{"TOOL", "DEFAULT MODEL", "DIRECT", "FILES"}, rows)
}

func printSinglePreviewHeader(u ui, item pendingPreview, env config.Env) {
	u.heading(toolLabel(item.target.out.tool) + " configuration")
	printFileSummary(u, 2, item, env)
	if item.preview != "" {
		u.line(2, "The preview is compact and redacted: generated model lists are summarised")
		u.line(2, "and credentials are hidden. The full diff is redacted the same way.")
		u.printf("\n")
		u.block(item.preview)
	}
}

func printFileSummary(u ui, indent int, item pendingPreview, env config.Env) {
	tool := item.target.out.tool
	for _, f := range item.write {
		_, _, existed, _ := inspectFile(f.Path)
		if !existed {
			u.line(indent, "Create "+f.Path)
		} else if b := item.names.picked[f.Path]; b != "" {
			u.line(indent, "Update "+f.Path)
			u.line(indent+2, "previous version kept as "+b)
		} else {
			u.line(indent, "Update "+f.Path)
		}
	}
	if item.prefChanged {
		u.line(
			indent,
			fmt.Sprintf(
				"Save launcher default model %s in %s",
				safe(item.prefAlias),
				config.ModelPrefsPath(env.UserHome),
			),
		)
	}
	if setupTools[tool].bin != "" {
		if item.target.out.asDefault {
			u.line(indent, "Direct invocation: Aiven becomes the default for "+toolLabel(tool))
		} else {
			u.line(indent, "Direct invocation: left as it is")
		}
	}
}

const (
	multiActionApply = iota
	multiActionInspect
	multiActionBack
	multiActionCancel
)

func promptMultiReview(deps Deps, p presenter, count int, allowBack bool) (int, error) {
	p.ui.printf("\n")
	applyDetail := fmt.Sprintf("write all %d tool configurations", count)
	if p.inline != nil {
		opts := []option{
			{label: "Apply all", detail: applyDetail},
			{label: "Inspect redacted diffs", detail: "view unified diffs, then return here"},
		}
		actions := []int{multiActionApply, multiActionInspect}
		if allowBack {
			opts = append(opts, option{label: "Back", detail: "change direct invocation or models"})
			actions = append(actions, multiActionBack)
		}
		opts = append(opts, option{label: "Cancel", detail: "leave keys and files unchanged"})
		actions = append(actions, multiActionCancel)
		c := choice{
			title:     "Apply configuration changes?",
			options:   opts,
			allowBack: allowBack,
			theme:     &p.ui.th,
		}
		picked, err := p.inline.Select(c)
		if err != nil {
			return 0, err
		}
		return actions[picked[0]], nil
	}
	p.ui.line(2, "Apply configuration changes:")
	p.ui.line(4, "1) Apply all ("+applyDetail+")")
	p.ui.line(4, "2) Inspect redacted diffs (view unified diffs, then return here)")
	maxOpt := 3
	if allowBack {
		p.ui.line(4, "3) Back (change direct invocation or models)")
		p.ui.line(4, "4) Cancel (leave keys and files unchanged)")
		maxOpt = 4
	} else {
		p.ui.line(4, "3) Cancel (leave keys and files unchanged)")
	}
	for range 3 {
		answer, err := deps.Prompter.ReadLine("Choose apply option (Enter for 1): ")
		if errors.Is(err, io.EOF) || errors.Is(err, errInputEnded) {
			return 0, errInputEnded
		}
		if err != nil {
			return 0, err
		}
		trimmed := strings.ToLower(strings.TrimSpace(answer))
		if trimmed == "" || trimmed == "y" || trimmed == "yes" {
			return multiActionApply, nil
		}
		if allowBack && (trimmed == "b" || trimmed == "back") {
			return multiActionBack, nil
		}
		if trimmed == "n" || trimmed == "no" || trimmed == "c" || trimmed == "cancel" {
			return multiActionCancel, nil
		}
		if n, err := strconv.Atoi(trimmed); err == nil && n >= 1 && n <= maxOpt {
			if !allowBack && n == 3 {
				return multiActionCancel, nil
			}
			return n - 1, nil
		}
		p.ui.line(2, fmt.Sprintf("Choose a number from 1 to %d.", maxOpt))
	}
	return 0, errors.New("no answer: expected apply option number")
}

func approveSingleInteractive(
	deps Deps,
	p presenter,
	item pendingPreview,
	table []models.Model,
	allowBack bool,
) ([]approvedWrite, reviewOutcome) {
	tool := item.target.out.tool
	showFull := func() error {
		all := append([]harness.File(nil), item.files...)
		for i := range all {
			all[i].Models = false
		}
		_, preview, err := changes(all, nil, table, item.names)
		if err != nil {
			return err
		}
		p.ui.printf("\n")
		p.ui.block(preview)
		return nil
	}
	if p.inline != nil {
		const (
			actApply = iota
			actFull
			actBack
			actCancel
		)
		opts := []option{{label: "Apply", detail: "write the files shown"}}
		acts := []int{actApply}
		if item.preview != "" {
			opts = append(opts, option{label: "Show full redacted diff", detail: "then ask again"})
			acts = append(acts, actFull)
		}
		if allowBack {
			opts = append(opts, option{label: "Back", detail: "change direct invocation or model"})
			acts = append(acts, actBack)
		}
		opts = append(opts, option{label: "Cancel", detail: "leave keys and files unchanged"})
		acts = append(acts, actCancel)
		c := choice{
			title:     "Apply these changes to " + toolLabel(tool) + "?",
			options:   opts,
			allowBack: allowBack,
			theme:     &p.ui.th,
		}
		for range 10 {
			p.ui.printf("\n")
			picked, err := p.inline.Select(c)
			if errors.Is(err, errBack) {
				return nil, reviewBack
			}
			if err != nil {
				item.target.out.status, item.target.out.reason = statusCancelled, "no files written"
				_ = fail(deps, tool+": "+err.Error())
				return nil, reviewCancelled
			}
			switch acts[picked[0]] {
			case actApply:
				return []approvedWrite{{
					target:    item.target,
					write:     item.write,
					names:     item.names,
					prefAlias: item.prefAlias,
				}}, reviewApproved
			case actFull:
				if err := showFull(); err != nil {
					item.target.out.status, item.target.out.reason = statusFailed, err.Error()
					_ = fail(deps, tool+": "+err.Error())
					return nil, reviewApproved
				}
			case actBack:
				return nil, reviewBack
			case actCancel:
				item.target.out.status, item.target.out.reason = statusDeclined, "no files written"
				return nil, reviewApproved
			}
		}
		item.target.out.status = statusFailed
		item.target.out.reason = "no answer: expected apply or cancel"
		_ = fail(deps, tool+": "+item.target.out.reason)
		return nil, reviewApproved
	}

	p.ui.printf("\n")
	if allowBack {
		p.ui.line(
			2,
			"Enter or y applies; n declines this tool; v shows the full redacted diff; b goes back.",
		)
	} else {
		p.ui.line(2, "Enter or y applies; n declines this tool; v shows the full redacted diff")
		p.ui.line(2, "and asks again.")
	}
	prompt := fmt.Sprintf("Apply this change to %s? [Y/n/v] ", tool)
	for range 10 {
		answer, err := deps.Prompter.ReadLine(prompt)
		if err != nil {
			item.target.out.status, item.target.out.reason = statusCancelled, "no files written"
			_ = fail(deps, tool+": "+errInputEnded.Error())
			return nil, reviewCancelled
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "y", "yes":
			return []approvedWrite{{
				target:    item.target,
				write:     item.write,
				names:     item.names,
				prefAlias: item.prefAlias,
			}}, reviewApproved
		case "n", "no":
			item.target.out.status, item.target.out.reason = statusDeclined, "no files written"
			return nil, reviewApproved
		case "b", "back":
			if allowBack {
				return nil, reviewBack
			}
		case "v", "view":
			if err := showFull(); err != nil {
				item.target.out.status, item.target.out.reason = statusFailed, err.Error()
				_ = fail(deps, tool+": "+err.Error())
				return nil, reviewApproved
			}
		default:
			p.ui.line(2, "Enter y to apply, n to decline, or v to show the full diff.")
		}
	}
	item.target.out.status, item.target.out.reason = statusFailed, "no answer: expected y, n or v"
	_ = fail(deps, tool+": "+item.target.out.reason)
	return nil, reviewApproved
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

// changes lists the files that differ from what is there now, or from over, the files already planned, with their
// preview.
func changes(
	files, over []harness.File,
	table []models.Model,
	names backupNames,
) ([]harness.File, string, error) {
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
