// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"strings"

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
			toolLabel(t.out.tool)+" default config cannot be edited"+nativeReason(t.nativeErr)+
				", so Aiven is available through the launch command only.",
		)
	}
	return offered
}

// nativeReason is " because " and what err says is wrong, without the file it names, the remedy it adds after a
// semicolon, or any parentheses and quotation marks; empty when err says nothing usable.
func nativeReason(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		msg = msg[i+2:]
	}
	msg, _, _ = strings.Cut(msg, ";")
	msg = strings.TrimSpace(strings.Map(func(r rune) rune {
		if strings.ContainsRune("()\"`\u201c\u201d", r) {
			return -1
		}
		return r
	}, safe(msg)))
	if msg == "" {
		return ""
	}
	return " because " + msg
}

// nativeDefaultFile names t's native default file for the default-provider prompt, with ~ for the home.
func nativeDefaultFile(t *setupTarget, env config.Env) string {
	return shortHomePath(env.UserHome, t.nativeFile)
}
