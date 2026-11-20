// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// selector is the inline choice list a terminal with cursor motion offers. Plain mode, a dumb terminal or
// NO_COLOR use numbered ReadLine prompts with the same choices instead.
type selector interface {
	// Select returns the indexes chosen; a multiple choice may return none. Esc returns errBack when c.allowBack
	// is true, else errCancelled. Ctrl-C returns errCancelled, end of input errInputEnded: none is ever a choice.
	Select(c choice) ([]int, error)
}

// choice is one inline question. Option labels and subtitles must already be safe for the terminal.
type choice struct {
	title         string
	subtitle      string
	options       []option
	multi         bool
	allowBack     bool
	filterDefault bool
	rail          bool
	theme         *theme
}

type option struct {
	label  string
	detail string
	picked bool
}

// selectState is the list's state between keys, kept apart from the terminal so its keys can be tested directly.
type selectState struct {
	c         choice
	th        theme
	filter    string
	filtering bool
	cursor    int
	offset    int
	picked    []bool
	input     []byte
}

func newSelectState(c choice) *selectState {
	th := defaultTheme()
	if c.theme != nil {
		th = *c.theme
	}
	picked := make([]bool, len(c.options))
	for i, o := range c.options {
		picked[i] = o.picked
	}
	return &selectState{
		c:         c,
		th:        th,
		filtering: c.filterDefault || len(c.options) > 8,
		picked:    picked,
	}
}

// visible lists the options whose label contains the filter, ignoring case.
func (s *selectState) visible() []int {
	if s.filter == "" {
		out := make([]int, len(s.c.options))
		for i := range s.c.options {
			out[i] = i
		}
		return out
	}
	needle := strings.ToLower(s.filter)
	var out []int
	for i, o := range s.c.options {
		if strings.Contains(strings.ToLower(o.label), needle) {
			out = append(out, i)
		}
	}
	return out
}

// viewport computes [start, end) within total visible rows so s.cursor stays in view and the rendered list
// (including optional ↑/↓ overflow lines) fits within avail rows.
func (s *selectState) viewport(total, avail int) (start, end int) {
	if avail < 1 {
		avail = 1
	}
	if total <= avail {
		s.offset = 0
		return 0, total
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	if s.cursor >= total {
		s.cursor = total - 1
	}
	if avail == 1 {
		s.offset = s.cursor
		return s.cursor, s.cursor + 1
	}
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if s.offset < 0 {
		s.offset = 0
	}
	windowAt := func(off int) int {
		top := 0
		if off > 0 {
			top = 1
		}
		if off+(avail-top) >= total {
			return avail - top
		}
		return max(1, avail-top-1)
	}
	if w := windowAt(s.offset); s.cursor >= s.offset+w {
		if s.cursor == total-1 {
			win := max(1, avail-1)
			s.offset = max(0, total-win)
		} else {
			win := max(1, avail-2)
			s.offset = max(0, s.cursor-win+1)
		}
	}
	if s.offset >= total {
		s.offset = max(0, total-1)
	}
	w := windowAt(s.offset)
	end = min(total, s.offset+w)
	return s.offset, end
}

func (s *selectState) render(width, height int) []string {
	g := s.th.g
	// Leave a 2-cell margin on wide terminals so East Asian Ambiguous glyphs never trigger unexpected soft-wrap.
	// On tiny widths (1..4 cells), drop the rail gutter so the active option still fits within width.
	useRail := s.c.rail && (width == 0 || width > 4)
	maxCells := 0
	if width > 2 {
		maxCells = width - 2
	} else if width > 0 {
		maxCells = width
	}
	titlePrefix, bodyPrefix := "", ""
	titleMax, bodyMax := maxCells, maxCells
	if useRail {
		titlePrefix = s.th.cyan(g.active) + "  "
		bodyPrefix = s.th.dim(g.railBar) + "  "
		if maxCells > 0 {
			titleMax = max(1, maxCells-cells(g.active)-2)
			bodyMax = max(1, maxCells-cells(g.railBar)-2)
		}
	}
	fitTitle := func(text string) string { return fitWith(text, titleMax, g.ellipsis) }
	fitBody := func(text string) string { return fitWith(text, bodyMax, g.ellipsis) }

	budget := 0
	if height > 0 {
		budget = max(1, height-1)
	}
	showFilter := s.filtering || s.filter != ""
	// Reserve at least 1 row for the active option before allocating rows to title, subtitle, filter or hint.
	var header []string
	if budget == 0 || len(header)+1 < budget {
		header = append(header, titlePrefix+s.th.bold(fitTitle(s.c.title)))
	}
	if s.c.subtitle != "" && (budget == 0 || len(header)+1 < budget) {
		header = append(header, bodyPrefix+s.th.dim(fitBody(s.c.subtitle)))
	}
	if showFilter && (budget == 0 || len(header)+1 < budget) {
		header = append(header, bodyPrefix+fitBody("  Filter: "+s.filter))
	}
	showHint := budget == 0 || len(header)+2 <= budget

	visible := s.visible()
	if len(visible) > 0 && s.cursor >= len(visible) {
		s.cursor = len(visible) - 1
	}
	labelWidth := 0
	for _, i := range visible {
		labelWidth = max(labelWidth, cells(s.c.options[i].label))
	}

	var body []string
	if len(visible) == 0 {
		body = append(body, bodyPrefix+fitBody("  No match; Backspace edits the filter"))
	} else {
		start, end := 0, len(visible)
		avail := len(visible)
		if budget > 0 {
			hintRows := 0
			if showHint {
				hintRows = 1
			}
			avail = max(1, budget-len(header)-hintRows)
			start, end = s.viewport(len(visible), avail)
		} else {
			s.offset = 0
		}
		wantTop := start > 0
		wantBot := end < len(visible)
		if budget > 0 {
			optRows := end - start
			if wantTop && optRows+1 > avail {
				wantTop = false
			}
			topRows := 0
			if wantTop {
				topRows = 1
			}
			if wantBot && optRows+topRows+1 > avail {
				wantBot = false
			}
		}
		if wantTop {
			body = append(body, bodyPrefix+s.th.dim(fitBody(fmt.Sprintf("  %s %d more", g.up, start))))
		}
		for n := start; n < end; n++ {
			idx := visible[n]
			body = append(
				body,
				bodyPrefix+renderOption(
					s.th,
					s.c.options[idx],
					n == s.cursor,
					s.c.multi,
					s.picked[idx],
					labelWidth,
					bodyMax,
				),
			)
		}
		if wantBot {
			body = append(
				body,
				bodyPrefix+s.th.dim(fitBody(fmt.Sprintf("  %s %d more", g.down, len(visible)-end))),
			)
		}
	}

	dot := " " + g.dot + " "
	var parts []string
	if s.filtering {
		parts = append(parts, g.up+"/"+g.down+" move")
	} else {
		parts = append(parts, g.up+"/"+g.down+"/j/k move")
	}
	if s.c.multi {
		parts = append(parts, "Space toggle")
		if !s.filtering {
			parts = append(parts, "a all")
		}
		parts = append(parts, "Enter continues")
	} else {
		parts = append(parts, "Enter chooses")
	}
	if showFilter {
		parts = append(parts, "type to filter")
	} else {
		parts = append(parts, "/ filter")
	}
	switch {
	case s.filter != "":
		parts = append(parts, "Esc clear")
	case s.c.allowBack:
		parts = append(parts, "Esc back")
	default:
		parts = append(parts, "Esc cancels")
	}
	hint := bodyPrefix + s.th.dim(fitBody(strings.Join(parts, dot)))

	lines := make([]string, 0, len(header)+len(body)+1)
	lines = append(lines, header...)
	lines = append(lines, body...)
	if showHint && (budget == 0 || len(lines) < budget) {
		lines = append(lines, hint)
	}
	return lines
}

func renderOption(
	th theme,
	o option,
	active, multi, picked bool,
	labelWidth, maxCells int,
) string {
	g := th.g
	markPlain, markStyled := "  ", "  "
	if active {
		markPlain = g.cursor + " "
		markStyled = th.boldCyan(g.cursor + " ")
	}
	box := ""
	if multi {
		box = "[ ] "
		if picked {
			box = "[x] "
		}
	}
	if maxCells > 0 {
		prefixCells := cells(markPlain) + cells(box)
		if maxCells <= prefixCells {
			return fitWith(markPlain+box, maxCells, g.ellipsis)
		}
		rem := maxCells - prefixCells
		label := pad(o.label, labelWidth)
		if o.detail == "" || cells(label) >= rem {
			shown := fitWith(o.label, rem, g.ellipsis)
			if active {
				return markStyled + box + th.bold(shown)
			}
			return markStyled + box + shown
		}
		remDetail := rem - cells(label)
		styledLabel := label
		if active {
			styledLabel = th.bold(o.label) + strings.Repeat(" ", max(0, labelWidth-cells(o.label)))
		}
		if remDetail <= 2 {
			return markStyled + box + styledLabel
		}
		return markStyled + box + styledLabel + "  " + th.dim(fitWith(o.detail, remDetail-2, g.ellipsis))
	}
	styledLabel := o.label
	if active {
		styledLabel = th.bold(o.label)
	}
	if o.detail == "" {
		return markStyled + box + styledLabel
	}
	gap := strings.Repeat(" ", max(0, labelWidth-cells(o.label)))
	return markStyled + box + styledLabel + gap + "  " + th.dim(o.detail)
}

func (s *selectState) cancelError() error {
	if s.c.allowBack {
		return errBack
	}
	return errCancelled
}

// handleStandaloneEscape resolves a timed-out standalone Escape key. If a non-empty filter is active, it clears the
// filter and returns true so the list stays open; otherwise it returns false to trigger cancelError.
func (s *selectState) handleStandaloneEscape() bool {
	s.input = nil
	if s.filter != "" {
		s.filter = ""
		s.cursor = 0
		s.offset = 0
		if !s.c.filterDefault && len(s.c.options) <= 8 {
			s.filtering = false
		}
		return true
	}
	return false
}

// keys applies input while retaining incomplete escape sequences and UTF-8 runes across reads.
// The terminal adapter bounds the wait for an escape suffix; finished reports an answer or cancellation.
func (s *selectState) keys(b []byte) (picked []int, finished bool, err error) {
	b = append(s.input, b...)
	s.input = nil
	for i := 0; i < len(b); i++ {
		visible := s.visible()
		switch c := b[i]; {
		case c == 0x1b:
			if i+1 == len(b) {
				s.input = append(s.input, b[i:]...)
				return nil, false, nil
			}
			if b[i+1] == 0x04 {
				return nil, true, errInputEnded
			}
			if b[i+1] == 0x1b {
				// Double-Esc respects allowBack (and clears any filter first).
				s.filter = ""
				s.cursor = 0
				s.offset = 0
				return nil, true, s.cancelError()
			}
			if b[i+1] != '[' && b[i+1] != 'O' {
				return nil, true, errCancelled
			}
			j := i + 2
			for j < len(b) && b[j] >= 0x20 && b[j] <= 0x3f {
				j++
			}
			if j == len(b) {
				if j-i >= 64 {
					return nil, true, errCancelled
				}
				s.input = append(s.input, b[i:]...)
				return nil, false, nil
			}
			if b[j] == 0x04 {
				return nil, true, errInputEnded
			}
			if b[j] < 0x40 || b[j] > 0x7e {
				return nil, true, errCancelled
			}
			switch b[j] {
			case 'A':
				if s.cursor > 0 {
					s.cursor--
				}
			case 'B':
				if s.cursor < len(visible)-1 {
					s.cursor++
				}
			case 'Z':
				if s.c.allowBack {
					return nil, true, errBack
				}
			}
			i = j
		case c == 0x03:
			return nil, true, errCancelled
		case c == 0x04:
			return nil, true, errInputEnded
		case c == '\r' || c == '\n':
			if s.c.multi {
				for j, p := range s.picked {
					if p {
						picked = append(picked, j)
					}
				}
				return picked, true, nil
			}
			if len(visible) > 0 {
				return []int{visible[s.cursor]}, true, nil
			}
		case c == ' ' && s.c.multi:
			if len(visible) > 0 {
				s.picked[visible[s.cursor]] = !s.picked[visible[s.cursor]]
			}
		case c == 0x7f || c == 0x08:
			if s.filter != "" {
				_, size := utf8.DecodeLastRuneInString(s.filter)
				s.filter, s.cursor, s.offset = s.filter[:len(s.filter)-size], 0, 0
			} else if s.filtering && !s.c.filterDefault && len(s.c.options) <= 8 {
				s.filtering = false
			}
		case c >= 0x20 && c != 0x7f:
			if !s.filtering {
				switch c {
				case 'j':
					if s.cursor < len(visible)-1 {
						s.cursor++
					}
				case 'k':
					if s.cursor > 0 {
						s.cursor--
					}
				case 'a':
					if s.c.multi && len(visible) > 0 {
						allPicked := true
						for _, idx := range visible {
							if !s.picked[idx] {
								allPicked = false
								break
							}
						}
						for _, idx := range visible {
							s.picked[idx] = !allPicked
						}
					}
				case '/':
					s.filtering = true
				}
				continue
			}
			if !utf8.FullRune(b[i:]) {
				s.input = append(s.input, b[i:]...)
				return nil, false, nil
			}
			r, size := utf8.DecodeRune(b[i:])
			if r != utf8.RuneError || size > 1 {
				s.filter, s.cursor, s.offset = s.filter+string(r), 0, 0
			}
			i += size - 1
		}
	}
	return nil, false, nil
}

func (s *selectState) pendingEscape() bool {
	return len(s.input) > 0 && s.input[0] == 0x1b
}

// chosen names the options at indexes, for the durable line printed after the list is erased.
func (c choice) chosen(indexes []int) string {
	var names []string
	for _, i := range indexes {
		names = append(names, c.options[i].label)
	}
	return strings.Join(slices.Clip(names), ", ")
}
