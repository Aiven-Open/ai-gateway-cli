// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

const (
	summaryLabelWidth = len("Default provider")
	backupNote        = "previous version kept as a backup beside it"
)

// summaryRow is one label of the review table and its values, the first beside the label, the rest below it.
type summaryRow struct {
	label  string
	values []string
	paths  bool
}

// printSummary shows what setup will configure for item, read from the plan it will write.
func printSummary(u ui, item pendingPreview, env config.Env, table []models.Model) {
	u.heading(toolLabel(item.target.out.tool))
	for _, row := range summaryRows(item, env, table) {
		label := row.label
		for _, v := range row.values {
			if row.paths {
				v = u.th.dim(v)
			}
			u.printf("  %s  %s\n", pad(label, summaryLabelWidth), v)
			label = ""
		}
	}
}

func summaryRows(item pendingPreview, env config.Env, table []models.Model) []summaryRow {
	t := item.target
	model := resolvedToolModel(t.out.tool, env, table, t.plan, t.planSel)
	rows := []summaryRow{{label: "Default model", values: []string{safe(model)}}}
	if t.out.tool == "claude" {
		if row, ok := claudeMenuRow(item.files, model); ok {
			rows = append(rows, row)
		}
	}
	provider := "not changed"
	if t.out.asDefault {
		provider = "Aiven"
	}
	rows = append(rows, summaryRow{label: "Default provider", values: []string{provider}})
	return append(rows, fileRow(item, env))
}

// claudeMenuRow lists the other models the dedicated settings file puts in /model.
func claudeMenuRow(files []harness.File, main string) (summaryRow, bool) {
	i := slices.IndexFunc(files, func(f harness.File) bool {
		return filepath.Base(f.Path) == filepath.Base(claude.SettingsFile)
	})
	if i < 0 {
		return summaryRow{}, false
	}
	menu, err := claude.ReadMenu(files[i].Data)
	if err != nil {
		return summaryRow{}, false
	}
	others := slices.DeleteFunc(menu.Others, func(m string) bool { return m == main })
	var values []string
	if len(others) > 0 {
		values = append(values, safe(strings.Join(others, ", ")))
	}
	switch {
	case menu.Discovery && len(values) > 0:
		values = append(values, "and every Claude model your key grants")
	case menu.Discovery:
		values = append(values, "every Claude model your key grants")
	}
	return summaryRow{label: "Also in /model", values: values}, len(values) > 0
}

// fileRow names each file setup writes and, under each one it replaces, that a backup is kept.
func fileRow(item pendingPreview, env config.Env) summaryRow {
	var values []string
	for _, f := range item.write {
		values = append(values, safe(shortHomePath(env.UserHome, f.Path)))
		if f.Existed && f.Backup {
			values = append(values, backupNote)
		}
	}
	count := len(item.write)
	if item.prefChanged {
		values = append(values, safe(shortHomePath(env.UserHome, config.ModelPrefsPath(env.UserHome))))
		count++
	}
	label := "File"
	if count > 1 {
		label = "Files"
	}
	return summaryRow{label: label, values: values, paths: true}
}
