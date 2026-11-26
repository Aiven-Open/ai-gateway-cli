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

// claudeRows are the review rows for Claude Code up to the provider: the default the settings file holds, the project
// pin beside it when one overrides it here, and the models the file adds to /model. effective is the model Claude Code
// would run in this directory, used only when the file names no default.
func claudeRows(
	item pendingPreview,
	env config.Env,
	table []models.Model,
	effective string,
) []summaryRow {
	menu, ok := claudeFileMenu(item.files)
	written := menu.Default
	if !ok || written == "" {
		written = effective
	}
	rows := []summaryRow{{label: "Default model", values: []string{safe(written)}}}
	if row, ok := projectPinRow(item, env, table, written); ok {
		rows = append(rows, row)
	}
	if row, ok := claudeMenuRow(menu, ok); ok {
		rows = append(rows, row)
	}
	return rows
}

// claudeFileMenu reads the menu from the dedicated settings file among files.
func claudeFileMenu(files []harness.File) (claude.Menu, bool) {
	i := slices.IndexFunc(files, func(f harness.File) bool {
		return filepath.Base(f.Path) == filepath.Base(claude.SettingsFile)
	})
	if i < 0 {
		return claude.Menu{}, false
	}
	menu, err := claude.ReadMenu(files[i].Data)
	return menu, err == nil
}

// projectPinRow names a model that a settings file under the working directory pins over the written default.
func projectPinRow(
	item pendingPreview,
	env config.Env,
	table []models.Model,
	written string,
) (summaryRow, bool) {
	planned := make(map[string][]byte, len(item.target.plan.Files))
	for _, f := range item.target.plan.Files {
		planned[f.Path] = f.Data
	}
	path, alias, _, err := claude.Effective(table, env.UserHome, env.WorkDir, planned)
	if err != nil || alias == "" || alias == written || env.WorkDir == "" {
		return summaryRow{}, false
	}
	rel, err := filepath.Rel(env.WorkDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return summaryRow{}, false
	}
	return summaryRow{
		label:  "In this project",
		values: []string{safe(alias + " from " + filepath.ToSlash(rel))},
	}, true
}

// claudeMenuRow lists the other models the dedicated settings file puts in /model.
func claudeMenuRow(menu claude.Menu, ok bool) (summaryRow, bool) {
	if !ok {
		return summaryRow{}, false
	}
	var values []string
	if len(menu.Others) > 0 {
		values = append(values, safe(strings.Join(menu.Others, ", ")))
	}
	switch {
	case menu.Discovery && len(values) > 0:
		values = append(values, "and every Claude model your key grants")
	case menu.Discovery:
		values = append(values, "every Claude model your key grants")
	}
	return summaryRow{label: "Also in /model", values: values}, len(values) > 0
}
