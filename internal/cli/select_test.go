// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

func toolChoice(multi bool) choice {
	return choice{title: "Tools", multi: multi, options: []option{
		{label: "Pi", detail: "pi"},
		{label: "OpenCode", detail: "opencode"},
		{label: "Codex", detail: "codex"},
	}}
}

func TestSelectKeysNeverTurnCancellationOrEndOfInputIntoAChoice(t *testing.T) {
	for _, tc := range []struct {
		name          string
		multi         bool
		filterDefault bool
		keys          []string
		picked        []int
		err           error
	}{
		{name: "enter picks the first", keys: []string{"\r"}, picked: []int{0}},
		{name: "arrows move", keys: []string{"\x1b[B", "\x1b[B", "\x1b[A", "\r"}, picked: []int{1}},
		{name: "application-mode arrows", keys: []string{"\x1bOB\r"}, picked: []int{1}},
		{name: "cursor stops at the end", keys: []string{"\x1b[B\x1b[B\x1b[B\x1b[B\r"}, picked: []int{2}},
		{name: "j and k move when not filtering", keys: []string{"jjk\r"}, picked: []int{1}},
		{name: "slash enters filter mode", keys: []string{"/dex\r"}, picked: []int{2}},
		{name: "filterDefault filters immediately", filterDefault: true, keys: []string{"dex", "\r"}, picked: []int{2}},
		{name: "filter ignores case", filterDefault: true, keys: []string{"OPEN\r"}, picked: []int{1}},
		{name: "backspace widens the filter", filterDefault: true, keys: []string{"cx", "\x7f", "\x7f", "\x7f", "\x1b[B\r"}, picked: []int{1}},
		{name: "enter on no match waits", filterDefault: true, keys: []string{"zz\r", "\x7f\x7f\r"}, picked: []int{0}},
		{name: "ctrl-c cancels", keys: []string{"\x1b[B", "\x03"}, err: errCancelled},
		{name: "ctrl-d ends input", keys: []string{"\x04"}, err: errInputEnded},
		{name: "multi: enter with nothing marked picks none", multi: true, keys: []string{"\r"}},
		{name: "multi: space marks", multi: true, keys: []string{" \x1b[B\x1b[B \r"}, picked: []int{0, 2}},
		{name: "multi: space unmarks", multi: true, keys: []string{"  \x1b[B \r"}, picked: []int{1}},
		{name: "multi: a toggles all then none", multi: true, keys: []string{"a\r"}, picked: []int{0, 1, 2}},
		{name: "multi: a twice clears all", multi: true, keys: []string{"aa\r"}},
		{name: "multi: marks survive filtering", multi: true, filterDefault: true, keys: []string{" ", "dex", " ", "\x7f\x7f\x7f\r"}, picked: []int{0, 2}},
		{name: "multi: ctrl-c after marking cancels", multi: true, keys: []string{" ", "\x03"}, err: errCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := toolChoice(tc.multi)
			c.filterDefault = tc.filterDefault
			s := newSelectState(c)
			var picked []int
			var finished bool
			var err error
			for _, k := range tc.keys {
				if picked, finished, err = s.keys([]byte(k)); finished {
					break
				}
			}
			if !finished || !errors.Is(err, tc.err) || !slices.Equal(picked, tc.picked) ||
				err != nil && picked != nil {
				t.Errorf(
					"picked %v, finished %v, err %v; want %v, %v",
					picked,
					finished,
					err,
					tc.picked,
					tc.err,
				)
			}
		})
	}
}

func TestSelectOptionPickedShiftTabAndDoubleEsc(t *testing.T) {
	t.Parallel()

	// option.picked initializes multi-select state cleanly.
	c := choice{
		title:     "Harnesses",
		multi:     true,
		allowBack: true,
		options: []option{
			{label: "Pi", picked: true},
			{label: "Codex", picked: false},
			{label: "Claude", picked: true},
		},
	}
	s := newSelectState(c)
	picked, finished, err := s.keys([]byte("\r"))
	if !finished || err != nil || !slices.Equal(picked, []int{0, 2}) {
		t.Fatalf("pre-picked Enter = %v, %v, %v; want [0 2]", picked, finished, err)
	}

	// Shift+Tab (\x1b[Z) returns errBack when allowBack is true.
	s = newSelectState(c)
	if _, finished, err := s.keys([]byte("\x1b[Z")); !finished || !errors.Is(err, errBack) {
		t.Fatalf("Shift+Tab with allowBack = finished:%v err:%v, want errBack", finished, err)
	}

	// Shift+Tab is ignored when allowBack is false.
	noBack := c
	noBack.allowBack = false
	s = newSelectState(noBack)
	if _, finished, err := s.keys([]byte("\x1b[Z")); finished || err != nil {
		t.Fatalf("Shift+Tab without allowBack = finished:%v err:%v, want open", finished, err)
	}

	// Double-Esc (\x1b\x1b) respects allowBack (errBack when true, errCancelled when false).
	s = newSelectState(c)
	if _, finished, err := s.keys([]byte("\x1b\x1b")); !finished || !errors.Is(err, errBack) {
		t.Fatalf("double-Esc with allowBack = finished:%v err:%v, want errBack", finished, err)
	}
	s = newSelectState(noBack)
	if _, finished, err := s.keys([]byte("\x1b\x1b")); !finished || !errors.Is(err, errCancelled) {
		t.Fatalf("double-Esc without allowBack = finished:%v err:%v, want errCancelled", finished, err)
	}

	// Two-stage standalone Esc clears non-empty filter first, then returns cancelError.
	fc := choice{
		title:         "Models",
		filterDefault: true,
		allowBack:     true,
		options:       []option{{label: "glm-5"}, {label: "gpt-sol"}},
	}
	s = newSelectState(fc)
	if _, finished, err := s.keys([]byte("glm")); finished || err != nil || s.filter != "glm" {
		t.Fatalf("filter = %q, finished=%v err=%v", s.filter, finished, err)
	}
	if !s.handleStandaloneEscape() || s.filter != "" {
		t.Fatalf("first standalone Esc should clear filter; filter=%q", s.filter)
	}
	if s.handleStandaloneEscape() {
		t.Fatal("second standalone Esc with empty filter should return false")
	}
	if !errors.Is(s.cancelError(), errBack) {
		t.Fatalf("cancelError = %v, want errBack", s.cancelError())
	}
}

func TestSelectKeysDoNotApproveIncompleteOrMalformedInput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		keys    []string
		pending bool
		err     error
	}{
		{name: "standalone Escape needs a bounded suffix wait", keys: []string{"\x1b"}, pending: true},
		{name: "incomplete CSI", keys: []string{"\x1b", "["}, pending: true},
		{name: "Ctrl-C after CSI", keys: []string{"\x1b[", "\x03"}, err: errCancelled},
		{name: "EOF after CSI", keys: []string{"\x1b[", "\x04"}, err: errInputEnded},
		{name: "EOF after Escape", keys: []string{"\x1b", "\x04"}, err: errInputEnded},
		{name: "Enter after CSI", keys: []string{"\x1b[", "\r"}, err: errCancelled},
		{name: "unknown Escape", keys: []string{"\x1b", "x\r"}, err: errCancelled},
		{name: "bounded prefix", keys: []string{"\x1b[" + strings.Repeat("1", 64)}, err: errCancelled},
		{name: "incomplete UTF-8", keys: []string{"\xe5\x90"}},
		{name: "Ctrl-C after UTF-8 prefix", keys: []string{"\xe5", "\x03"}, err: errCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := toolChoice(false)
			c.filterDefault = true
			s := newSelectState(c)
			var picked []int
			var finished bool
			var err error
			for _, key := range tc.keys {
				picked, finished, err = s.keys([]byte(key))
			}
			if len(picked) != 0 || finished != (tc.err != nil) || !errors.Is(err, tc.err) ||
				s.pendingEscape() != tc.pending {
				t.Fatalf(
					"picked=%v finished=%v err=%v pendingEscape=%v",
					picked,
					finished,
					err,
					s.pendingEscape(),
				)
			}
		})
	}
}

func TestSelectKeysDecodeFragmentsAcrossReads(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		keys   []string
		picked []int
		filter string
	}{
		{name: "CSI prefix", keys: []string{"\x1b[", "B", "\r"}, picked: []int{1}},
		{name: "CSI every byte", keys: []string{"\x1b", "[", "B", "\r"}, picked: []int{1}},
		{name: "application every byte", keys: []string{"\x1b", "O", "B", "\r"}, picked: []int{1}},
		{name: "modified arrow", keys: []string{"\x1b[1;", "5", "B", "\r"}, picked: []int{1}},
		{name: "up and down", keys: []string{"\x1b[B\x1b", "[", "B\x1bO", "A", "\r"}, picked: []int{1}},
		{name: "UTF-8 every byte", keys: []string{"\xe5", "\x90", "\x8d", "\r"}, picked: []int{3}, filter: "名"},
		{name: "UTF-8 backspace", keys: []string{"\xe5\x90", "\x8d", "\x7f", "\r"}, picked: []int{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := toolChoice(false)
			c.filterDefault = true
			c.options = append(c.options, option{label: "名前"})
			s := newSelectState(c)
			for i, key := range tc.keys {
				picked, finished, err := s.keys([]byte(key))
				if i < len(tc.keys)-1 {
					if finished || err != nil || len(picked) != 0 {
						t.Fatalf("fragment %d ended question: %v, %v, %v", i, picked, finished, err)
					}
					continue
				}
				if !finished || err != nil || !slices.Equal(picked, tc.picked) || s.filter != tc.filter {
					t.Fatalf("picked=%v finished=%v err=%v filter=%q; want %v, %q",
						picked, finished, err, s.filter, tc.picked, tc.filter)
				}
			}
		})
	}
}

func TestSelectKeysAreIndependentOfChunkBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input string
		multi       bool
		picked      []int
	}{
		{name: "modified CSI arrow", input: "\x1b[1;5B\r", picked: []int{1}},
		{name: "application arrow", input: "\x1bOB\r", picked: []int{1}},
		{name: "UTF-8 filter", input: "名\r", picked: []int{3}},
		{name: "four-byte UTF-8 filter", input: "😀\r", picked: []int{4}},
		{name: "marked choices", input: " \x1b[B \r", multi: true, picked: []int{0, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for mask := 0; mask < 1<<(len(tc.input)-1); mask++ {
				c := toolChoice(tc.multi)
				c.filterDefault = true
				c.options = append(c.options, option{label: "名前"}, option{label: "😀"})
				s := newSelectState(c)
				start := 0
				for i := range len(tc.input) {
					if i != len(tc.input)-1 && mask&(1<<i) == 0 {
						continue
					}
					picked, finished, err := s.keys([]byte(tc.input[start : i+1]))
					start = i + 1
					if i != len(tc.input)-1 {
						if finished || err != nil {
							t.Fatalf("partition %b ended early: picked=%v err=%v", mask, picked, err)
						}
					} else if !finished || err != nil || !slices.Equal(picked, tc.picked) {
						t.Fatalf("partition %b: picked=%v finished=%v err=%v; want %v",
							mask, picked, finished, err, tc.picked)
					}
				}
			}
		})
	}
}

func TestSelectRenderFitsTheWidthAndShowsMarks(t *testing.T) {
	ansi := regexp.MustCompile("\x1b\\[[0-9;]*m")
	s := newSelectState(toolChoice(true))
	if _, finished, err := s.keys([]byte(" \x1b[B")); finished || err != nil {
		t.Fatalf("selection ended: finished=%v err=%v", finished, err)
	}
	lines := s.render(24, 0)
	if len(lines) != 5 || !strings.Contains(lines[1], "  [x] Pi") ||
		!strings.HasPrefix(lines[2], "\x1b[1;36m❯ \x1b[0m[ ] \x1b[1mOpenCode\x1b[0m") {
		t.Fatalf("lines %q", lines)
	}
	for _, l := range lines {
		if plain := ansi.ReplaceAllString(l, ""); cells(plain) > 22 {
			t.Errorf("%q is %d cells, wider than 22 (width-2 margin)", plain, cells(plain))
		}
	}
	if _, finished, err := s.keys([]byte("/zz")); finished || err != nil {
		t.Fatalf("filter ended: finished=%v err=%v", finished, err)
	}
	if lines := s.render(80, 0); !strings.Contains(strings.Join(lines, "\n"), "No match") {
		t.Errorf("no-match lines %q", lines)
	}
}

func TestSelectRenderViewportClamping(t *testing.T) {
	t.Parallel()
	var opts []option
	for i := range 45 {
		opts = append(opts, option{label: fmt.Sprintf("model-%02d", i), detail: "provider"})
	}
	s := newSelectState(choice{
		title:         "Select a model",
		options:       opts,
		filterDefault: true,
		allowBack:     true,
		rail:          true,
	})
	// 14-row terminal -> must render <= 13 lines at every cursor position.
	for _, moves := range []int{0, 12, 25, 44} {
		s.cursor = moves
		lines := s.render(80, 14)
		if len(lines) > 13 {
			t.Fatalf("cursor=%d rendered %d lines, exceeds height-1 (13)", moves, len(lines))
		}
		joined := strings.Join(lines, "\n")
		wantActive := fmt.Sprintf("model-%02d", moves)
		if !strings.Contains(joined, wantActive) {
			t.Errorf("cursor=%d active row %q missing from viewport:\n%s", moves, wantActive, joined)
		}
		if moves == 0 && !strings.Contains(joined, "↓ ") {
			t.Errorf("top viewport missing down indicator:\n%s", joined)
		}
		if moves == 25 && (!strings.Contains(joined, "↑ ") || !strings.Contains(joined, "↓ ")) {
			t.Errorf("middle viewport missing both up and down indicators:\n%s", joined)
		}
		if moves == 44 && !strings.Contains(joined, "↑ ") {
			t.Errorf("bottom viewport missing up indicator:\n%s", joined)
		}
	}
	// Tiny pane never panics and stays within height-1.
	for _, h := range []int{2, 3, 4, 5} {
		lines := s.render(40, h)
		if len(lines) > max(1, h-1) {
			t.Errorf("height=%d rendered %d lines, want <= %d", h, len(lines), max(1, h-1))
		}
	}
}

func TestFitCountsCellsAndCutsOnGraphemes(t *testing.T) {
	for _, tc := range []struct {
		in    string
		width int
		want  string
	}{
		{"short", 10, "short"},
		{"abcdefgh", 5, "abcd…"},
		{"日本語テキスト", 7, "日本語…"},
		{"éééé", 3, "éé…"},
		{"👩‍👩‍👧 family", 4, "👩‍👩‍👧 …"},
		{"anything", 0, "anything"},
	} {
		if got := fit(tc.in, tc.width); got != tc.want || tc.width > 0 && cells(got) > tc.width {
			t.Errorf("fit(%q, %d) = %q (%d cells), want %q", tc.in, tc.width, got, cells(got), tc.want)
		}
	}
}

func TestSafeDropsTerminalControlButKeepsTabsAndText(t *testing.T) {
	in := "a\x1b[2Jb\x9bc\rd\u202ee\u2066f\tg\x00h名"
	if got, want := safe(in), "a[2Jbcdef\tgh名"; got != want {
		t.Errorf("safe(%q) = %q, want %q", in, got, want)
	}
}

func TestRenderModelsColumnsOnlyWhenTheTerminalIsWideEnough(t *testing.T) {
	table := []models.Model{
		{Alias: "claude-opus-5-5"}, {Alias: "claude-sonnet-5-5"}, {Alias: "claude-haiku-4-5"},
		{Alias: "evil\x1b]0;pwned\x07", Owner: "Mallory\x1b[31m"},
		{Alias: "wide", Owner: "公司", Name: "全角の名前"},
	}
	for _, tc := range []struct {
		name  string
		width int
		want  string
	}{
		{"redirected", 0, "Anthropic\n" +
			"  Claude Haiku 4.5   claude-haiku-4-5\n" +
			"  Claude Opus 5.5    claude-opus-5-5\n" +
			"  Claude Sonnet 5.5  claude-sonnet-5-5\n" +
			"\nMallory[31m\n  evil]0;pwned\n" +
			"\n公司\n  全角の名前  wide\n"},
		{"narrow terminal", 40, "Anthropic\n" +
			"  Claude Haiku 4.5   claude-haiku-4-5\n" +
			"  Claude Opus 5.5    claude-opus-5-5\n" +
			"  Claude Sonnet 5.5  claude-sonnet-5-5\n" +
			"\nMallory[31m\n  evil]0;pwned\n" +
			"\n公司\n  全角の名前  wide\n"},
		{"wide terminal", 120, "Anthropic\n" +
			"  Claude Haiku 4.5   claude-haiku-4-5     Claude Opus 5.5    claude-opus-5-5      Claude Sonnet 5.5  claude-sonnet-5-5\n" +
			"\nMallory[31m\n  evil]0;pwned\n" +
			"\n公司\n  全角の名前  wide\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			renderModels(ui{w: &b, width: tc.width}, table)
			if b.String() != tc.want {
				t.Errorf("got\n%s\nwant\n%s", b.String(), tc.want)
			}
			for _, l := range strings.Split(b.String(), "\n") {
				if tc.width > 0 && cells(l) > tc.width {
					t.Errorf("%q is wider than %d", l, tc.width)
				}
			}
		})
	}
}
