// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

func toolChoice(multi bool) choice {
	return choice{title: "Tools", multi: multi, options: []option{
		{label: "Pi", detail: "pi"}, {label: "OpenCode", detail: "opencode"}, {label: "Codex", detail: "codex"},
	}}
}

func TestSelectKeysNeverTurnCancellationOrEndOfInputIntoAChoice(t *testing.T) {
	for _, tc := range []struct {
		name   string
		multi  bool
		keys   []string
		picked []int
		err    error
	}{
		{name: "enter picks the first", keys: []string{"\r"}, picked: []int{0}},
		{name: "arrows move", keys: []string{"\x1b[B", "\x1b[B", "\x1b[A", "\r"}, picked: []int{1}},
		{name: "application-mode arrows", keys: []string{"\x1bOB\r"}, picked: []int{1}},
		{name: "cursor stops at the end", keys: []string{"\x1b[B\x1b[B\x1b[B\x1b[B\r"}, picked: []int{2}},
		{name: "filter then enter", keys: []string{"dex", "\r"}, picked: []int{2}},
		{name: "filter ignores case", keys: []string{"OPEN\r"}, picked: []int{1}},
		{name: "backspace widens the filter", keys: []string{"cx", "\x7f", "\x7f", "\x7f", "\x1b[B\r"}, picked: []int{1}},
		{name: "enter on no match waits", keys: []string{"zz\r", "\x7f\x7f\r"}, picked: []int{0}},
		{name: "ctrl-c cancels", keys: []string{"\x1b[B", "\x03"}, err: errCancelled},
		{name: "ctrl-d ends input", keys: []string{"\x04"}, err: errInputEnded},
		{name: "multi: enter with nothing marked picks none", multi: true, keys: []string{"\r"}},
		{name: "multi: space marks", multi: true, keys: []string{" \x1b[B\x1b[B \r"}, picked: []int{0, 2}},
		{name: "multi: space unmarks", multi: true, keys: []string{"  \x1b[B \r"}, picked: []int{1}},
		{name: "multi: marks survive filtering", multi: true, keys: []string{" ", "dex", " ", "\x7f\x7f\x7f\r"}, picked: []int{0, 2}},
		{name: "multi: ctrl-c after marking cancels", multi: true, keys: []string{" ", "\x03"}, err: errCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSelectState(toolChoice(tc.multi))
			var picked []int
			var finished bool
			var err error
			for _, k := range tc.keys {
				if picked, finished, err = s.keys([]byte(k)); finished {
					break
				}
			}
			if !finished || !errors.Is(err, tc.err) || !slices.Equal(picked, tc.picked) || err != nil && picked != nil {
				t.Errorf("picked %v, finished %v, err %v; want %v, %v", picked, finished, err, tc.picked, tc.err)
			}
		})
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
			s := newSelectState(toolChoice(false))
			var picked []int
			var finished bool
			var err error
			for _, key := range tc.keys {
				picked, finished, err = s.keys([]byte(key))
			}
			if len(picked) != 0 || finished != (tc.err != nil) || !errors.Is(err, tc.err) ||
				s.pendingEscape() != tc.pending {
				t.Fatalf("picked=%v finished=%v err=%v pendingEscape=%v", picked, finished, err, s.pendingEscape())
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
	s := newSelectState(toolChoice(true))
	if _, finished, err := s.keys([]byte(" \x1b[B")); finished || err != nil {
		t.Fatalf("selection ended: finished=%v err=%v", finished, err)
	}
	lines := s.render(24)
	if len(lines) != 6 || !strings.Contains(lines[2], "  [x] Pi") || !strings.HasPrefix(lines[3], "> [ ] OpenCode") {
		t.Fatalf("lines %q", lines)
	}
	for _, l := range lines {
		if plain := strings.NewReplacer("\x1b[1m", "", "\x1b[2m", "", "\x1b[0m", "").Replace(l); cells(plain) > 23 {
			t.Errorf("%q is %d cells, wider than 23", plain, cells(plain))
		}
	}
	if _, finished, err := s.keys([]byte("zz")); finished || err != nil {
		t.Fatalf("filter ended: finished=%v err=%v", finished, err)
	}
	if lines := s.render(80); !strings.Contains(strings.Join(lines, "\n"), "No match") {
		t.Errorf("no-match lines %q", lines)
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
