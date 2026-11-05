// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// selector is the inline choice list a terminal with cursor motion offers. Plain mode, a dumb terminal or
// NO_COLOR use numbered ReadLine prompts with the same choices instead.
type selector interface {
	// Select returns the indexes chosen; a multiple choice may return none. Esc and Ctrl-C return errCancelled,
	// end of input errInputEnded: neither is ever a choice.
	Select(c choice) ([]int, error)
}

// choice is one inline question. Option labels must already be safe for the terminal.
type choice struct {
	title   string
	options []option
	multi   bool
}

type option struct{ label, detail string }

// selectState is the list's state between keys, kept apart from the terminal so its keys can be tested directly.
type selectState struct {
	c      choice
	filter string
	cursor int
	picked []bool
	input  []byte
}

func newSelectState(c choice) *selectState {
	return &selectState{c: c, picked: make([]bool, len(c.options))}
}

// visible lists the options whose label contains the filter, ignoring case.
func (s *selectState) visible() []int {
	var out []int
	for i, o := range s.c.options {
		if strings.Contains(strings.ToLower(o.label), strings.ToLower(s.filter)) {
			out = append(out, i)
		}
	}
	return out
}

func (s *selectState) render(width int) []string {
	line := func(text string) string { return fit(text, width-1) }
	lines := []string{"\x1b[1m" + line(s.c.title) + "\x1b[0m", line("  Filter: " + s.filter)}
	visible := s.visible()
	labelWidth := 0
	for _, i := range visible {
		labelWidth = max(labelWidth, cells(s.c.options[i].label))
	}
	for n, i := range visible {
		o, mark := s.c.options[i], "  "
		if n == s.cursor {
			mark = "> "
		}
		box := ""
		if s.c.multi {
			box = "[ ] "
			if s.picked[i] {
				box = "[x] "
			}
		}
		text := mark + box + pad(o.label, labelWidth)
		if o.detail != "" {
			text += "  " + o.detail
		}
		lines = append(lines, line(text))
	}
	if len(visible) == 0 {
		lines = append(lines, line("  No match; Backspace edits the filter"))
	}
	hint := "↑/↓ move · Enter chooses · type to filter · Esc cancels"
	if s.c.multi {
		hint = "↑/↓ move · Space selects · Enter continues · type to filter · Esc cancels"
	}
	return append(lines, "\x1b[2m"+line(hint)+"\x1b[0m")
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
			if b[j] == 'A' && s.cursor > 0 {
				s.cursor--
			} else if b[j] == 'B' && s.cursor < len(visible)-1 {
				s.cursor++
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
				s.filter, s.cursor = s.filter[:len(s.filter)-size], 0
			}
		case c >= 0x20 && c != 0x7f:
			if !utf8.FullRune(b[i:]) {
				s.input = append(s.input, b[i:]...)
				return nil, false, nil
			}
			r, size := utf8.DecodeRune(b[i:])
			if r != utf8.RuneError || size > 1 {
				s.filter, s.cursor = s.filter+string(r), 0
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
