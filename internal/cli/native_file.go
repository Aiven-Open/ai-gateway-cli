// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
)

// nativeFileOf is the file setup edits to make Aiven a tool's default, as inspection found it: the file only that
// choice adds to the write set, else the first file setup edits, which is the native one for OpenCode and Codex.
func nativeFileOf(snapshot, launcherOnly harness.Plan) string {
	dedicated := make(map[string]bool, len(launcherOnly.Files))
	for _, f := range launcherOnly.Files {
		dedicated[f.Path] = true
	}
	for _, f := range snapshot.Files {
		if !dedicated[f.Path] {
			return f.Path
		}
	}
	if len(snapshot.Files) > 0 {
		return snapshot.Files[0].Path
	}
	return ""
}

// withholdUneditable drops the tools whose native default cannot be edited, since choosing Aiven as their default
// would fail, and says why in one line each. It never names the file, which setup will not write.
func withholdUneditable(p presenter, eligible []*setupTarget) []*setupTarget {
	var offered []*setupTarget
	for _, t := range eligible {
		if t.nativeErr == nil {
			offered = append(offered, t)
			continue
		}
		p.ui.line(
			2,
			toolLabel(
				t.out.tool,
			)+" default config cannot be edited, so Aiven is available through the launch command only.",
		)
	}
	return offered
}

// nativeDefaultFile names t's native default file for the default-provider prompt, with ~ for the home.
func nativeDefaultFile(t *setupTarget, env config.Env) string {
	return shortHomePath(env.UserHome, t.nativeFile)
}
