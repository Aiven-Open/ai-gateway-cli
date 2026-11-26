// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"github.com/aiven-open/ai-gateway-cli/internal/config"
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
			u.verbatim("  " + pad(label, summaryLabelWidth) + "  " + v + "\n")
			label = ""
		}
	}
}

func summaryRows(item pendingPreview, env config.Env, table []models.Model) []summaryRow {
	t := item.target
	model := resolvedToolModel(t.out.tool, env, table, t.plan, t.planSel)
	rows := []summaryRow{{label: "Default model", values: []string{safe(model)}}}
	if t.out.tool == "claude" {
		rows = claudeRows(item, env, table, model)
	}
	provider := "not changed"
	if t.out.asDefault {
		provider = "Aiven"
	}
	rows = append(rows, summaryRow{label: "Default provider", values: []string{provider}})
	return append(rows, fileRow(item, env))
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
