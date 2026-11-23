// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"runtime"
	"strings"
)

// glyphs holds the visual markers used by the vertical rail and inline selector.
// ASCII fallbacks are selected in plain mode or non-UTF-8 locales (LC_ALL=C/POSIX).
type glyphs struct {
	railStart string
	railBar   string
	railEnd   string
	active    string
	done      string
	ok        string
	fail      string
	warn      string
	cursor    string
	ellipsis  string
	up        string
	down      string
	dot       string
	spinner   []string
}

var utf8Glyphs = glyphs{
	railStart: "┌",
	railBar:   "│",
	railEnd:   "└",
	active:    "◆",
	done:      "◇",
	ok:        "✓",
	fail:      "✗",
	warn:      "!",
	cursor:    "❯",
	ellipsis:  "…",
	up:        "↑",
	down:      "↓",
	dot:       "·",
	spinner:   []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
}

var asciiGlyphs = glyphs{
	railStart: "+",
	railBar:   "|",
	railEnd:   "+",
	active:    "*",
	done:      "-",
	ok:        "+",
	fail:      "x",
	warn:      "!",
	cursor:    ">",
	ellipsis:  "...",
	up:        "^",
	down:      "v",
	dot:       "|",
	spinner:   []string{"-", `\`, "|", "/"},
}

// theme styles text using standard ANSI-16 SGR codes only (1 bold, 2 dim, 31 red, 32 green, 33 yellow, 36 cyan) so
// output adapts naturally to light and dark terminals with zero OSC 11 queries. In plain mode or on non-TTY streams,
// every style method is an identity function emitting zero escape sequences.
type theme struct {
	color bool
	g     glyphs
}

func newTheme(color, utf8 bool) theme {
	g := asciiGlyphs
	if utf8 && color {
		g = utf8Glyphs
	}
	return theme{color: color, g: g}
}

func defaultTheme() theme {
	return theme{color: true, g: utf8Glyphs}
}

const (
	bold   = "1"
	dim    = "2"
	red    = "31"
	green  = "32"
	yellow = "33"
	cyan   = "36"
)

func (t theme) paint(code, s string) string {
	if !t.color || code == "" || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (t theme) bold(s string) string     { return t.paint(bold, s) }
func (t theme) dim(s string) string      { return t.paint(dim, s) }
func (t theme) green(s string) string    { return t.paint(green, s) }
func (t theme) cyan(s string) string     { return t.paint(cyan, s) }
func (t theme) boldCyan(s string) string { return t.paint(bold+";"+cyan, s) }

// utf8Locale reports whether the environment supports UTF-8 glyphs. Explicit C/POSIX or non-UTF-8 locales return
// false so ASCII glyphs are used instead; an unset locale defaults to UTF-8 on modern Unix and Windows terminals.
func utf8Locale(environ []string) bool {
	var lcAll, lcCtype, lang string
	for _, kv := range environ {
		name, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch name {
		case "LC_ALL":
			lcAll = v
		case "LC_CTYPE":
			lcCtype = v
		case "LANG":
			lang = v
		}
	}
	loc := lcAll
	if loc == "" {
		loc = lcCtype
	}
	if loc == "" {
		loc = lang
	}
	if loc == "" {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(loc))
	if lower == "c" || lower == "posix" {
		return false
	}
	if runtime.GOOS == "windows" && lower == "" {
		return true
	}
	return strings.Contains(lower, "utf-8") || strings.Contains(lower, "utf8")
}
