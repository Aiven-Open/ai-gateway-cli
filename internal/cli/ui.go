// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"

	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

// Terminal describes what each standard stream is attached to. The streams are detected separately: stdout can be a
// pipe while prompts and progress still reach a terminal on stderr.
type Terminal struct {
	StdinTTY, StdoutTTY, StderrTTY bool
	// NoVT is true when the interactive stderr/stdin console cannot enable virtual terminal processing.
	NoVT bool
	// StdoutNoVT is true when stdout is a terminal that cannot enable virtual terminal processing.
	StdoutNoVT bool
	// StdoutWidth and StderrWidth return the stream's current width in cells, or 0 when unknown.
	StdoutWidth, StderrWidth func() int
}

// plainRequested reports a nonempty NO_COLOR or TERM=dumb, which rule out styling and cursor motion.
func plainRequested(environ []string) bool {
	for _, kv := range environ {
		name, v, _ := strings.Cut(kv, "=")
		if name == "NO_COLOR" && v != "" || name == "TERM" && v == "dumb" {
			return true
		}
	}
	return false
}

// ui renders already redacted, human-facing text to one stream. Words and layout carry every meaning; colour only
// reinforces them, so a plain transcript reads the same.
type ui struct {
	w     io.Writer
	color bool
	th    theme
	// width is the terminal's width in cells, or 0 when the stream is not a terminal: layout never follows COLUMNS
	// or a guess there, so redirected output is the same everywhere.
	width int
}

func newUI(w io.Writer, tty bool, width func() int, plain bool) ui {
	return newUIWithEnv(w, tty, width, plain, nil)
}

func newUIWithEnv(w io.Writer, tty bool, width func() int, plain bool, environ []string) ui {
	color := tty && !plain
	u := ui{
		w:     w,
		color: color,
		th:    newTheme(color, utf8Locale(environ)),
	}
	if tty && width != nil {
		u.width = width()
	}
	return u
}

func (u ui) paint(code, s string) string { return u.th.paint(code, s) }

func (u ui) printf(format string, args ...any) { _, _ = fmt.Fprintf(u.w, format, args...) }

// heading starts a section after a blank line.
func (u ui) heading(s string) { u.printf("\n%s\n", u.th.bold(safe(s))) }

// line prints one indented line of plain text.
func (u ui) line(indent int, s string) { u.printf("%s%s\n", strings.Repeat(" ", indent), safe(s)) }

// note prints one indented line of dimmed text.
func (u ui) note(indent int, s string) {
	u.printf("%s%s\n", strings.Repeat(" ", indent), u.th.dim(safe(s)))
}

// status prints a status word, coloured by kind, then its detail.
func (u ui) status(indent int, kind, word, detail string) {
	if detail != "" {
		detail = " " + safe(detail)
	}
	u.printf("%s%s%s\n", strings.Repeat(" ", indent), u.paint(kind, word), detail)
}

// railStart opens the vertical rail with a bold header.
func (u ui) railStart(title string) {
	if !u.color {
		u.line(0, title)
		return
	}
	u.printf("%s  %s\n", u.th.dim(u.th.g.railStart), u.th.bold(safe(title)))
}

// railPrefix returns the vertical rail gutter prefix for spinner and body lines.
func (u ui) railPrefix() string {
	if !u.color {
		return "  "
	}
	return u.th.dim(u.th.g.railBar) + "  "
}

// railNote prints one dimmed body line inside the vertical rail.
func (u ui) railNote(text string) {
	if !u.color {
		u.line(2, text)
		return
	}
	u.printf("%s  %s\n", u.th.dim(u.th.g.railBar), u.th.dim(safe(text)))
}

// railDone prints a collapsed completed beat on the vertical rail.
func (u ui) railDone(label, summary string) {
	if !u.color {
		if summary == "" {
			u.line(0, label)
		} else {
			u.line(0, label+": "+summary)
		}
		return
	}
	if summary == "" {
		u.printf("%s  %s\n", u.th.dim(u.th.g.done), u.th.bold(safe(label)))
		return
	}
	u.printf(
		"%s  %s %s %s\n",
		u.th.dim(u.th.g.done),
		u.th.bold(safe(label)),
		u.th.dim(u.th.g.dot),
		safe(summary),
	)
}

// railStatus prints a coloured status glyph and message inside the vertical rail.
func (u ui) railStatus(kind, glyph, text string) {
	if !u.color {
		word := "OK"
		switch kind {
		case red:
			word = "FAILED"
		case yellow:
			word = "WARN"
		}
		u.line(2, word+" "+text)
		return
	}
	u.printf("%s  %s  %s\n", u.th.dim(u.th.g.railBar), u.paint(kind, glyph), safe(text))
}

func (u ui) railOK(text string)   { u.railStatus(green, u.th.g.ok, text) }
func (u ui) railWarn(text string) { u.railStatus(yellow, u.th.g.warn, text) }
func (u ui) railFail(text string) { u.railStatus(red, u.th.g.fail, text) }

// railEnd closes the vertical rail cleanly before copy-pasteable tables, diffs or commands.
func (u ui) railEnd(text string) {
	if !u.color {
		if text != "" {
			u.heading(text)
		}
		return
	}
	if text == "" {
		u.printf("%s\n", u.th.dim(u.th.g.railEnd))
		return
	}
	u.printf("%s  %s\n", u.th.dim(u.th.g.railEnd), u.th.bold(safe(text)))
}

// command either preserves the executable text exactly or refuses to display it. Sanitizing a quoted token
// would silently change its argument; tabs are also unrepresentable when copied from a terminal.
func (u ui) command(indent int, s string) bool {
	if s == "" || strings.ContainsRune(s, '\t') || safe(s) != s {
		return false
	}
	u.printf("%s%s\n", strings.Repeat(" ", indent), u.th.cyan(s))
	return true
}

// block writes multi-line text such as a redacted diff, sanitising each line and colouring diff markers.
func (u ui) block(text string) {
	for line := range strings.Lines(text) {
		line = strings.TrimSuffix(line, "\n")
		code := ""
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			code = bold
		case strings.HasPrefix(line, "+"):
			code = green
		case strings.HasPrefix(line, "-"):
			code = red
		case strings.HasPrefix(line, "@@"):
			code = cyan
		}
		u.printf("%s\n", u.paint(code, safe(line)))
	}
}

// safe drops control characters, including escape sequence introducers and bidirectional overrides, so text from a
// catalogue, a config file or the environment cannot move the cursor, recolour or reorder what the terminal shows.
// Tabs stay, since diffs of indented files hold them.
func safe(s string) string {
	clean := true
	for _, r := range s {
		if r != '\t' &&
			(unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || r == utf8.RuneError) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r != '\t' &&
			(unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || r == utf8.RuneError) {
			return -1
		}
		return r
	}, s)
}

// cells is the terminal width of s: wide East Asian characters and emoji take two cells, combining marks none.
func cells(s string) int { return uniseg.StringWidth(s) }

// fit cuts s to at most width cells on a grapheme boundary, marking a cut with an ellipsis.
func fit(s string, width int) string { return fitWith(s, width, "…") }

func fitWith(s string, width int, ellipsis string) string {
	if width <= 0 || cells(s) <= width {
		return s
	}
	ew := cells(ellipsis)
	if width < ew {
		return ""
	}
	if width == ew {
		return ellipsis
	}
	var b strings.Builder
	used, state := 0, -1
	for rest := s; rest != ""; {
		var cluster string
		var w int
		cluster, rest, w, state = uniseg.FirstGraphemeClusterInString(rest, state)
		if used+w > width-ew {
			break
		}
		b.WriteString(cluster)
		used += w
	}
	return b.String() + ellipsis
}

func pad(s string, width int) string {
	if n := width - cells(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// renderModels writes the catalogue grouped by owner or publisher. Each entry leads with its display name, then the
// exact id harness flags take. A terminal wide enough gets several columns; anything else gets one entry per line.
func renderModels(u ui, table []models.Model) {
	for i, g := range models.Grouped(table) {
		if i > 0 {
			u.printf("\n")
		}
		u.printf("%s\n", u.th.bold(safe(g.Name)))
		// The readable name leads; the exact id harness flags take follows it, once when the two are the same.
		nameWidth := 0
		for _, m := range g.Models {
			nameWidth = max(nameWidth, cells(safe(m.DisplayName())))
		}
		entries, plain := make([]string, len(g.Models)), make([]string, len(g.Models))
		cellWidth := 0
		for j, m := range g.Models {
			id, name := safe(m.Alias), safe(m.DisplayName())
			plain[j], entries[j] = id, id
			if name != id {
				plain[j] = pad(name, nameWidth) + "  " + id
				entries[j] = pad(name, nameWidth) + "  " + u.th.dim(id)
			}
			cellWidth = max(cellWidth, cells(plain[j]))
		}
		const indent, gap = 2, 4
		cols := 1
		if u.width > 0 {
			cols = max(1, (u.width-indent+gap)/(cellWidth+gap))
		}
		rows := (len(entries) + cols - 1) / cols
		for r := range rows {
			u.printf("%s", strings.Repeat(" ", indent))
			for c := range cols {
				j := c*rows + r
				if j >= len(entries) {
					break
				}
				u.printf("%s", entries[j])
				if next := (c+1)*rows + r; next < len(entries) {
					u.printf("%s", strings.Repeat(" ", cellWidth-cells(plain[j])+gap))
				}
			}
			u.printf("\n")
		}
	}
}
