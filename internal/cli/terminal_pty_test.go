// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build darwin || linux

package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// ptyPair is a pseudo-terminal whose program side the real terminal code uses; the test types on the other side
// and reads what the terminal showed.
type ptyPair struct {
	ptmx, tty *os.File
	mu        sync.Mutex
	shown     strings.Builder
	done      chan struct{}
	changed   chan struct{}
}

func openPTY(t *testing.T) *ptyPair {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	p := &ptyPair{ptmx: ptmx, tty: tty, done: make(chan struct{}), changed: make(chan struct{}, 1)}
	go func() {
		defer close(p.done)
		buf := make([]byte, 256)
		for {
			n, err := ptmx.Read(buf)
			p.mu.Lock()
			p.shown.Write(buf[:n])
			p.mu.Unlock()
			select {
			case p.changed <- struct{}{}:
			default:
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = tty.Close()
		_ = ptmx.Close()
		<-p.done
	})
	return p
}

func (p *ptyPair) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.shown.String()
}

// waitFor waits for terminal output containing s; the deadline only bounds a broken run.
func (p *ptyPair) waitFor(t *testing.T, s string) {
	t.Helper()
	p.waitForCount(t, s, 1)
}

func (p *ptyPair) waitForCount(t *testing.T, s string, count int) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for strings.Count(p.output(), s) < count {
		select {
		case <-p.changed:
		case <-timer.C:
			t.Fatalf("terminal never showed %q %d times; it showed %q", s, count, p.output())
		}
	}
}

func (p *ptyPair) typeKeys(t *testing.T, keys string) {
	t.Helper()
	if _, err := p.ptmx.WriteString(keys); err != nil {
		t.Fatal(err)
	}
}

func termios(t *testing.T, f *os.File) *unix.Termios {
	t.Helper()
	tio, err := unix.IoctlGetTermios(int(f.Fd()), getTermios)
	if err != nil {
		t.Fatal(err)
	}
	return tio
}

// cooked reports the line discipline a shell leaves: echo, line editing, signals and newline translation.
func cooked(tio *unix.Termios) bool {
	return tio.Lflag&unix.ECHO != 0 && tio.Lflag&unix.ICANON != 0 && tio.Lflag&unix.ISIG != 0 && tio.Oflag&unix.OPOST != 0
}

// probedWriter forwards to the terminal and records its modes at each write, so ordering is checked at the moment
// text reaches the screen rather than by timing.
type probedWriter struct {
	t      *testing.T
	tty    *os.File
	writes []string
	modes  []*unix.Termios
}

type failingChoiceWriter struct {
	tty    *os.File
	writes int
	failAt int
	short  bool
}

func (w *failingChoiceWriter) Write(b []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		if w.short {
			return 0, nil
		}
		return 0, io.ErrClosedPipe
	}
	return w.tty.Write(b)
}

func TestPTYInlineSelectRejectsPresentationFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		keys   string
		failAt int
		short  bool
	}{
		{name: "invisible question", keys: "\r", failAt: 1},
		{name: "erase before redraw", keys: "p\r", failAt: 2},
		{name: "redraw", keys: "p", failAt: 3},
		{name: "erase before consent", keys: "\r", failAt: 2},
		{name: "short question write", keys: "\r", failAt: 1, short: true},
		{name: "short erase write", keys: "\r", failAt: 2, short: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := openPTY(t)
			before := *termios(t, p.tty)
			w := &failingChoiceWriter{tty: p.tty, failAt: tc.failAt, short: tc.short}
			prompter := terminalPrompter{in: p.tty, stderr: w, lines: bufio.NewReader(p.tty)}
			type result struct {
				picked []int
				err    error
			}
			got := make(chan result, 1)
			go func() {
				picked, err := prompter.Select(toolChoice(false))
				got <- result{picked, err}
			}()
			if tc.failAt != 1 {
				p.waitFor(t, "Esc cancels")
			} else {
				// Queue Enter even when the writer prevents the question from being seen.
				// The test must observe an output error, never consent from that queued key.
				p.typeKeys(t, tc.keys)
			}
			if tc.failAt != 1 {
				p.typeKeys(t, tc.keys[:1])
			}
			select {
			case r := <-got:
				wantErr := io.ErrClosedPipe
				if tc.short {
					wantErr = io.ErrShortWrite
				}
				if len(r.picked) != 0 || !errors.Is(r.err, wantErr) {
					t.Errorf("unshown question accepted: picked=%v err=%v", r.picked, r.err)
				}
			case <-time.After(time.Second):
				p.typeKeys(t, "\x03")
				<-got
				t.Fatal("presentation failure left Select waiting for input")
			}
			if after := *termios(t, p.tty); after != before {
				t.Errorf("modes after output failure = %+v, want %+v", after, before)
			}
		})
	}
}

func (w *probedWriter) Write(b []byte) (int, error) {
	w.writes = append(w.writes, string(b))
	w.modes = append(w.modes, termios(w.t, w.tty))
	return w.tty.Write(b)
}

func TestPTYSecretIsHiddenBeforeThePromptAndRestoredBeforeTheNewline(t *testing.T) {
	for _, tc := range []struct {
		name, keys, want string
		err              error
	}{
		{name: "pasted key", keys: "sk-pty-secret\r", want: "sk-pty-secret"},
		{name: "enter alone", keys: "\r", want: ""},
		{name: "end of input", keys: "\x04", err: errInputEnded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := openPTY(t)
			if !cooked(termios(t, p.tty)) {
				t.Fatal("pseudo-terminal did not start cooked")
			}
			w := &probedWriter{t: t, tty: p.tty}
			prompter := terminalPrompter{in: p.tty, stderr: w, lines: bufio.NewReader(p.tty)}
			type result struct {
				s   string
				err error
			}
			got := make(chan result, 1)
			go func() {
				s, err := prompter.ReadSecret("Key: ")
				got <- result{s, err}
			}()
			p.waitFor(t, "Key: ")
			p.typeKeys(t, tc.keys)
			r := <-got
			if r.s != tc.want || !errors.Is(r.err, tc.err) || tc.err == nil && r.err != nil {
				t.Fatalf("ReadSecret = %q, %v; want %q, %v", r.s, r.err, tc.want, tc.err)
			}
			if len(w.writes) != 2 || w.writes[0] != "Key: " || w.writes[1] != "\n" {
				t.Fatalf("writes %q, want the prompt then one newline", w.writes)
			}
			if w.modes[0].Lflag&unix.ECHO != 0 {
				t.Error("echo was still on when the prompt appeared")
			}
			if !cooked(w.modes[1]) {
				t.Error("the terminal was not restored before the final newline")
			}
			if !cooked(termios(t, p.tty)) {
				t.Error("the terminal was left in raw or silent mode")
			}
			p.waitFor(t, "Key: \r\n")
			if strings.Contains(p.output(), "sk-pty") {
				t.Errorf("the key was echoed: %q", p.output())
			}
		})
	}
}

func TestPTYInlineSelectRestoresTheTerminalAndUsesNoAlternateScreen(t *testing.T) {
	for _, tc := range []struct {
		name, keys string
		multi      bool
		picked     []int
		err        error
	}{
		{name: "move and choose", keys: "\x1b[B\r", picked: []int{1}},
		{name: "mark two", multi: true, keys: " \x1b[B\x1b[B \r", picked: []int{0, 2}},
		{name: "esc", keys: "\x1b", err: errCancelled},
		{name: "ctrl-c", keys: "\x03", err: errCancelled},
		{name: "ctrl-d", keys: "\x04", err: errInputEnded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := openPTY(t)
			before := *termios(t, p.tty)
			prompter := terminalPrompter{in: p.tty, stderr: p.tty, lines: bufio.NewReader(p.tty)}
			type result struct {
				picked []int
				err    error
			}
			got := make(chan result, 1)
			go func() {
				picked, err := prompter.Select(toolChoice(tc.multi))
				got <- result{picked, err}
			}()
			p.waitFor(t, "Esc cancels")
			// Also cover keys queued together, as a fast typist or a paste sends them.
			p.typeKeys(t, tc.keys)
			r := <-got
			if !errors.Is(r.err, tc.err) || tc.err == nil && r.err != nil || !slices.Equal(r.picked, tc.picked) {
				t.Fatalf("Select = %v, %v; want %v, %v", r.picked, r.err, tc.picked, tc.err)
			}
			// Exact equality. This descriptor is polled, which clears darwin's PENDIN; the child test covers that flag.
			if after := *termios(t, p.tty); after != before {
				t.Errorf("modes after Select = %+v, want %+v", after, before)
			}
			if out := p.output(); strings.Contains(out, "\x1b[?1049h") || strings.Contains(out, "\x1b[?47h") {
				t.Errorf("switched to the alternate screen: %q", out)
			}
		})
	}
}

type choiceDrawWriter struct {
	tty   *os.File
	draws chan struct{}
}

func (w choiceDrawWriter) Write(b []byte) (int, error) {
	n, err := w.tty.Write(b)
	if err == nil && strings.Contains(string(b), "Esc cancels") {
		w.draws <- struct{}{}
	}
	return n, err
}

func waitForChoiceDraw(t *testing.T, draws <-chan struct{}) {
	t.Helper()
	select {
	case <-draws:
	case <-time.After(time.Second):
		t.Fatal("selector did not draw after input")
	}
}

func TestPTYInlineSelectDecodesSeparateWrites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		keys   []string
		picked []int
		err    error
	}{
		{name: "CSI", keys: []string{"\x1b", "[", "B", "\r"}, picked: []int{1}},
		{name: "CSI prefix", keys: []string{"\x1b[", "B", "\r"}, picked: []int{1}},
		{name: "application", keys: []string{"\x1b", "O", "B", "\r"}, picked: []int{1}},
		{name: "modified CSI", keys: []string{"\x1b[1", ";", "5B", "\r"}, picked: []int{1}},
		{name: "UTF-8", keys: []string{"\xe5", "\x90", "\x8d", "\r"}, picked: []int{3}},
		{name: "Ctrl-C after CSI", keys: []string{"\x1b[", "\x03"}, err: errCancelled},
		{name: "EOF after CSI", keys: []string{"\x1b[", "\x04"}, err: errInputEnded},
		{name: "Escape after arrow", keys: []string{"\x1b[B", "\x1b"}, err: errCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := openPTY(t)
			before := *termios(t, p.tty)
			draws := make(chan struct{}, 16)
			prompter := terminalPrompter{in: p.tty, stderr: choiceDrawWriter{p.tty, draws}, lines: bufio.NewReader(p.tty)}
			c := toolChoice(false)
			c.options = append(c.options, option{label: "名前"})
			type result struct {
				picked []int
				err    error
			}
			got := make(chan result, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				picked, err := prompter.Select(c)
				got <- result{picked, err}
			}()
			t.Cleanup(func() {
				select {
				case <-done:
				default:
					p.typeKeys(t, "\x03")
					<-done
				}
			})
			waitForChoiceDraw(t, draws)
			for i, keys := range tc.keys {
				p.typeKeys(t, keys)
				if i < len(tc.keys)-1 {
					// Each redraw proves the previous fragment was read before the next write.
					waitForChoiceDraw(t, draws)
				}
			}
			select {
			case r := <-got:
				if !errors.Is(r.err, tc.err) || !slices.Equal(r.picked, tc.picked) {
					t.Errorf("Select = %v, %v; want %v, %v", r.picked, r.err, tc.picked, tc.err)
				}
			case <-time.After(time.Second):
				t.Fatal("fragmented input did not finish")
			}
			if after := *termios(t, p.tty); after != before {
				t.Errorf("modes after fragmented input = %+v, want %+v", after, before)
			}
		})
	}
}

func TestPTYInlineEscapeTimeoutNeverApproves(t *testing.T) {
	for _, keys := range []string{"\x1b", "\x1b[", "\x1bO", " \x1b[1;"} {
		t.Run(fmt.Sprintf("%q", keys), func(t *testing.T) {
			p := openPTY(t)
			before := *termios(t, p.tty)
			prompter := terminalPrompter{in: p.tty, stderr: p.tty, lines: bufio.NewReader(p.tty)}
			type result struct {
				picked []int
				err    error
			}
			got := make(chan result, 1)
			go func() {
				picked, err := prompter.Select(toolChoice(true))
				got <- result{picked, err}
			}()
			p.waitFor(t, "Esc cancels")
			start := time.Now()
			p.typeKeys(t, keys)
			select {
			case r := <-got:
				if len(r.picked) != 0 || !errors.Is(r.err, errCancelled) {
					t.Errorf("Escape accepted: picked=%v err=%v", r.picked, r.err)
				}
				if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
					t.Errorf("Escape prefix cancelled immediately after %s", elapsed)
				}
			case <-time.After(time.Second):
				p.typeKeys(t, "\x03")
				<-got
				t.Fatal("Escape wait was not bounded")
			}
			if after := *termios(t, p.tty); after != before {
				t.Errorf("modes after Escape timeout = %+v, want %+v", after, before)
			}
		})
	}
}

// The Ctrl-C path exits the process, so it runs in a child whose controlling terminal is the pseudo-terminal.
func TestPTYCtrlCDuringSecretInputRestoresTheTerminalAndExits130(t *testing.T) {
	if os.Getenv("AIG_PTY_CHILD") == "1" {
		_, _ = TerminalPrompter(os.Stdin, os.Stderr).ReadSecret("Key: ")
		os.Exit(0)
	}
	p := openPTY(t)
	before := *termios(t, p.ptmx)
	cmd := exec.Command(os.Args[0], "-test.run=^TestPTYCtrlCDuringSecretInputRestoresTheTerminalAndExits130$")
	cmd.Env = append(os.Environ(), "AIG_PTY_CHILD=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = p.tty, p.tty, p.tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p.waitFor(t, "Key: ")
	// Ctrl-C is a signal only once the line discipline has it on again for the read.
	for deadline := time.Now().Add(5 * time.Second); termios(t, p.tty).Lflag&unix.ISIG == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the read never enabled signals")
		}
	}
	if termios(t, p.tty).Lflag&unix.ECHO != 0 {
		t.Error("echo on during secret input")
	}
	p.typeKeys(t, "sk-partial\x03")
	err := cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 130 {
		t.Fatalf("child ended with %v, want exit 130", err)
	}
	// The exiting session leader revoked every descriptor for the terminal side, and opening it anew resets the
	// modes; the controlling side still reads the modes the child left.
	if after := *termios(t, p.ptmx); after != before {
		t.Errorf("modes after Ctrl-C = %+v, want %+v", after, before)
	}
	if strings.Contains(p.output(), "sk-partial") {
		t.Errorf("partial key echoed: %q", p.output())
	}
}

// The child reads the terminal as a launched binary does. Two things would clear darwin's PENDIN and hide a restore
// that leaves it set: a descriptor this process watches, and the flush when a session leader's exit revokes it. Esc ends the list without an answer; an external
// SIGINT, which raw mode never turns into a key, restores the modes and exits 130.
func TestPTYInlineSelectInAChildRestoresTheExactModes(t *testing.T) {
	if os.Getenv("AIG_PTY_CHILD") == "1" {
		picked, err := TerminalPrompter(os.Stdin, os.Stderr).(terminalPrompter).Select(toolChoice(false))
		_, _ = fmt.Fprintf(os.Stderr, "answered %v %v\n", picked, err)
		os.Exit(0)
	}
	for _, tc := range []struct {
		name, keys, output string
		signal             bool
		code               int
	}{
		{name: "esc", keys: "\x1b", output: "answered [] cancelled", code: 0},
		{name: "sigint", signal: true, code: 130},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := openPTY(t)
			before := *termios(t, p.ptmx)
			cmd := exec.Command(os.Args[0], "-test.run=^TestPTYInlineSelectInAChildRestoresTheExactModes$")
			cmd.Env = append(os.Environ(), "AIG_PTY_CHILD=1")
			cmd.Stdin, cmd.Stdout, cmd.Stderr = p.tty, p.tty, p.tty
			// No controlling terminal: a session leader's exit revokes the terminal, and that flush clears PENDIN.
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			// A plain duplicate keeps the terminal side open after the child exits, as the user's shell does. The
			// os.File is closed: Go watches it with kqueue, and the readiness check that wakes would clear PENDIN.
			keep, err := unix.Dup(int(p.tty.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = unix.Close(keep) })
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			_ = p.tty.Close()
			p.waitFor(t, "Esc cancels")
			if tc.signal {
				if err := cmd.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			} else {
				p.typeKeys(t, tc.keys)
			}
			err = cmd.Wait()
			code := 0
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != tc.code {
				t.Fatalf("child exit %d, want %d; output %q", code, tc.code, p.output())
			}
			if after := *termios(t, p.ptmx); after != before {
				t.Errorf("modes after the list = %+v, want %+v", after, before)
			}
			if out := p.output(); tc.output == "" && strings.Contains(out, "answered") || !strings.Contains(out, tc.output) {
				t.Errorf("output %q, want %q", out, tc.output)
			}
		})
	}
}

// External signals must also restore modes while waiting for a fragmented arrow's suffix.
func TestPTYSignalDuringInlineSelectRestoresTheTerminalAndExits130(t *testing.T) {
	if os.Getenv("AIG_PTY_CHILD") == "1" {
		picked, err := TerminalPrompter(os.Stdin, os.Stderr).(terminalPrompter).Select(toolChoice(false))
		_, _ = fmt.Fprintf(os.Stderr, "answered %v %v\n", picked, err)
		os.Exit(0)
	}
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			p := openPTY(t)
			before := *termios(t, p.ptmx)
			cmd := exec.Command(os.Args[0], "-test.run=^TestPTYSignalDuringInlineSelectRestoresTheTerminalAndExits130$")
			cmd.Env = append(os.Environ(), "AIG_PTY_CHILD=1")
			cmd.Stdin, cmd.Stdout, cmd.Stderr = p.tty, p.tty, p.tty
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			})
			p.waitFor(t, "Esc cancels")
			// Exercise the signal while waiting for the suffix of a fragmented arrow.
			p.typeKeys(t, "\x1b[")
			p.waitForCount(t, "Esc cancels", 2)
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			err := cmd.Wait()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 130 {
				t.Fatalf("child ended with %v, want exit 130", err)
			}
			if after := *termios(t, p.ptmx); after != before {
				t.Errorf("modes after %v = %+v, want %+v", sig, after, before)
			}
			if strings.Contains(p.output(), "answered") {
				t.Errorf("an interrupted list returned an answer: %q", p.output())
			}
		})
	}
}
