// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/execx"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
)

// scriptedSelector answers inline choices from a queue; an exhausted queue is end of input. ReadLine must not be
// reached while the inline list is in use.
type scriptedSelector struct {
	answers []selectAnswer
	asked   []choice
	lines   int
}

type selectAnswer struct {
	picked []int
	err    error
}

func (s *scriptedSelector) ReadSecret(string) (string, error) { return "", io.EOF }

func (s *scriptedSelector) ReadLine(string) (string, error) {
	s.lines++
	return "", io.EOF
}

func (s *scriptedSelector) Select(c choice) ([]int, error) {
	s.asked = append(s.asked, c)
	if len(s.answers) == 0 {
		return nil, errInputEnded
	}
	a := s.answers[0]
	s.answers = s.answers[1:]
	return a.picked, a.err
}

type allInstalled struct{}

func (allInstalled) LookPath(name string) (string, error)  { return "/usr/bin/" + name, nil }
func (allInstalled) Exec(string, []string, []string) error { return nil }

var _ execx.Runner = allInstalled{}

func inlineDeps(sel Prompter, stderr io.Writer) Deps {
	return Deps{Stderr: stderr, Stdout: io.Discard, Prompter: sel, Runner: allInstalled{},
		Term: Terminal{StdinTTY: true, StderrTTY: true, StderrWidth: func() int { return 80 }}}
}

func TestInlineApprovalAppliesOnlyOnAnExplicitApply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	files := []harness.File{{Path: path, Data: []byte("{\"apiKey\":\"SYNTHETIC_SECRET\"}\n")}}
	for _, tc := range []struct {
		name    string
		answers []selectAnswer
		ok      bool
		outcome reviewOutcome
		diffs   int
	}{
		{name: "apply", answers: []selectAnswer{{picked: []int{0}}}, ok: true, outcome: reviewApproved},
		{name: "decline/cancel", answers: []selectAnswer{{picked: []int{2}}}, outcome: reviewApproved},
		{name: "file changes then apply", answers: []selectAnswer{{picked: []int{1}}, {picked: []int{0}}}, ok: true, outcome: reviewApproved, diffs: 1},
		{name: "esc", answers: []selectAnswer{{err: errCancelled}}, outcome: reviewCancelled},
		{name: "file changes then ctrl-c", answers: []selectAnswer{{picked: []int{1}}, {err: errCancelled}}, outcome: reviewCancelled, diffs: 1},
		{name: "end of input", outcome: reviewCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sel := &scriptedSelector{answers: tc.answers}
			var stderr strings.Builder
			deps := inlineDeps(sel, &stderr)
			p := newPresenter(config.Env{}, deps, false)
			if p.inline == nil {
				t.Fatal("inline list not offered on a terminal")
			}
			item := pendingPreview{
				target: &setupTarget{out: &targetOutcome{tool: "pi"}},
				files:  files,
				write:  files,
				names:  backupNames{time.Unix(0, 0), map[string]string{}},
			}
			approved, outcome := approveInteractive(deps, p, []pendingPreview{item}, false)
			if (len(approved) == 1) != tc.ok || outcome != tc.outcome || sel.lines != 0 {
				t.Errorf(
					"approved %d, outcome %v, ReadLine calls %d; want ok=%v, outcome=%v, 0",
					len(approved),
					outcome,
					sel.lines,
					tc.ok,
					tc.outcome,
				)
			}
			if got := strings.Count(stderr.String(), "+++ "+path); got != tc.diffs {
				t.Errorf("%d full diffs, want %d: %q", got, tc.diffs, stderr.String())
			}
			if strings.Contains(stderr.String(), "SYNTHETIC_SECRET") {
				t.Error("the full diff showed the credential")
			}
		})
	}
}

func TestInlineLaunchPickerCancelLaunchesNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer selectAnswer
		code   int
		tail   string
	}{
		{name: "cancel", answer: selectAnswer{err: errCancelled}, tail: "Nothing launched.\n"},
		{name: "end of input", code: 1, tail: errInputEnded.Error() + "\n"},
		{name: "second tool", answer: selectAnswer{picked: []int{1}}, code: 1, tail: ". Run: aiven-ai-gateway setup " + toolNames[1] + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sel := &scriptedSelector{}
			if tc.answer.picked != nil || tc.answer.err != nil {
				sel.answers = []selectAnswer{tc.answer}
			}
			var stderr strings.Builder
			deps := inlineDeps(sel, &stderr)
			env := config.Env{UserHome: t.TempDir(), BaseURL: "https://gateway.example.invalid"}
			if code := launchCommand(
				context.Background(),
				nil,
				env,
				deps,
			); code != tc.code ||
				!strings.HasSuffix(stderr.String(), tc.tail) {
				t.Fatalf("exit %d, stderr %q; want %d ending %q", code, stderr.String(), tc.code, tc.tail)
			}
			if len(sel.asked) != 1 || sel.asked[0].multi || len(sel.asked[0].options) != len(toolNames) ||
				sel.lines != 0 {
				t.Errorf("asked %+v, ReadLine calls %d", sel.asked, sel.lines)
			}
		})
	}
}

func TestPlainModeUsesNumberedPromptsEvenOnATerminal(t *testing.T) {
	for _, environ := range [][]string{{"NO_COLOR=1"}, {"TERM=dumb"}, nil} {
		sel := &scriptedSelector{}
		deps := inlineDeps(sel, io.Discard)
		plain := environ == nil
		if p := newPresenter(config.Env{Environ: environ}, deps, plain); p.inline != nil {
			t.Errorf("environ %q, --plain %v: inline list offered", environ, plain)
		}
	}
	deps := inlineDeps(&scriptedSelector{}, io.Discard)
	deps.Term.StderrTTY = false
	if p := newPresenter(config.Env{}, deps, false); p.inline != nil {
		t.Error("inline list offered with stderr redirected")
	}
}

func TestInlineToolPickerReportsCancellationApartFromNone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer selectAnswer
		want   []string
		err    error
	}{
		{name: "two picked", answer: selectAnswer{picked: []int{0, 3}}, want: []string{"pi", "claude"}},
		{name: "none", answer: selectAnswer{}},
		{name: "cancel", answer: selectAnswer{err: errCancelled}, err: errCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sel := &scriptedSelector{answers: []selectAnswer{tc.answer}}
			var stderr strings.Builder
			deps := inlineDeps(sel, &stderr)
			var candidates []*setupTarget
			for _, name := range toolNames {
				candidates = append(candidates, &setupTarget{out: &targetOutcome{tool: name}, badge: "new"})
			}
			got, err := pickToolsBeat1(deps, newPresenter(config.Env{}, deps, false), candidates, nil, nil)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") || !errors.Is(err, tc.err) ||
				!sel.asked[0].multi {
				t.Errorf("got %v, %v; want %v, %v", got, err, tc.want, tc.err)
			}
			if len(tc.want) > 0 && !strings.Contains(stderr.String(), "Pi, Claude Code") {
				t.Errorf("no durable record of the choice: %q", stderr.String())
			}
		})
	}
}
