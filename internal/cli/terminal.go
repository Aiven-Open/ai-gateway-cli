// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

var (
	// errInputEnded is end of input where an answer was needed; it never counts as consent.
	errInputEnded = errors.New("no answer: input ended")
	// errCancelled is Esc or Ctrl-C in an inline choice.
	errCancelled = errors.New("cancelled")
)

// DetectTerminal checks each stream on its own, so a redirected stdout does not change how stderr is drawn.
func DetectTerminal(in, out, errOut *os.File) Terminal {
	width := func(f *os.File) func() int {
		return func() int {
			w, _, err := term.GetSize(int(f.Fd()))
			if err != nil {
				return 0
			}
			return w
		}
	}
	return Terminal{
		StdinTTY: term.IsTerminal(int(in.Fd())), StdoutTTY: term.IsTerminal(int(out.Fd())),
		StderrTTY: term.IsTerminal(int(errOut.Fd())), StdoutWidth: width(out), StderrWidth: width(errOut),
	}
}

type terminalPrompter struct {
	in     *os.File
	stderr io.Writer
	lines  *bufio.Reader
}

// TerminalPrompter returns a silent prompter when in is a terminal, else nil (stdin is read as a line).
func TerminalPrompter(in *os.File, stderr io.Writer) Prompter {
	if !term.IsTerminal(int(in.Fd())) {
		return nil
	}
	return terminalPrompter{in: in, stderr: stderr, lines: bufio.NewReader(in)}
}

func (p terminalPrompter) ReadSecret(prompt string) (string, error) {
	fd := int(p.in.Fd())
	state, err := term.GetState(fd)
	if err != nil {
		return "", err
	}
	defer p.interruptible(fd, state)()
	// Disable echo before inviting a paste, not once the prompt is already showing.
	if err := silence(fd); err != nil {
		return "", err
	}
	defer func() { restoreModes(fd, state) }()
	if _, err := fmt.Fprint(p.stderr, prompt); err != nil {
		return "", err
	}
	b, err := readHidden(fd)
	// Restore before the newline, so it is shown in the mode the next output assumes.
	restoreModes(fd, state)
	_, _ = fmt.Fprintln(p.stderr)
	if errors.Is(err, io.EOF) {
		return "", errInputEnded
	}
	return string(b), err
}

func (p terminalPrompter) ReadLine(prompt string) (string, error) {
	if _, err := fmt.Fprint(p.stderr, prompt); err != nil {
		return "", err
	}
	// A line cut short by end of input still reports io.EOF, so a confirmation never reads it as consent.
	line, err := p.lines.ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

// Select draws an inline list below the cursor and redraws it in place on each key; nothing switches to the
// alternate screen. The list is erased before returning, so the caller prints the durable outcome.
func (p terminalPrompter) Select(c choice) ([]int, error) {
	fd := int(p.in.Fd())
	state, err := term.GetState(fd)
	if err != nil {
		return nil, err
	}
	defer p.interruptible(fd, state)()
	if _, err := term.MakeRaw(fd); err != nil {
		return nil, err
	}
	defer func() { restoreModes(fd, state) }()
	s := newSelectState(c)
	drawn := 0
	write := func(operation, text string) error {
		n, err := io.WriteString(p.stderr, text)
		if err != nil {
			return fmt.Errorf("%s choice: %w", operation, err)
		}
		if n != len(text) {
			return fmt.Errorf("%s choice: %w", operation, io.ErrShortWrite)
		}
		return nil
	}
	erase := func() error {
		if drawn > 0 {
			if err := write("erase", fmt.Sprintf("\r\x1b[%dA\x1b[J", drawn)); err != nil {
				return err
			}
			drawn = 0
		}
		return nil
	}
	draw := func() error {
		width, _, err := term.GetSize(fd)
		if err != nil {
			width = 0
		}
		if err := erase(); err != nil {
			return err
		}
		lines := s.render(width)
		// Output processing is off in raw mode, so each line ends with an explicit carriage return.
		text := strings.Join(lines, "\r\n") + "\r\n"
		if err := write("draw", text); err != nil {
			return err
		}
		drawn = len(lines)
		return nil
	}
	if err := draw(); err != nil {
		return nil, err
	}
	pending := make([]byte, p.lines.Buffered())
	_, _ = io.ReadFull(p.lines, pending)
	buf := make([]byte, 64)
	var escapeDeadline time.Time
	for {
		chunk := pending
		pending = nil
		if len(chunk) == 0 {
			if !escapeDeadline.IsZero() {
				ready, err := waitChoiceInput(fd, escapeDeadline)
				if err != nil {
					return nil, errors.Join(err, erase())
				}
				if !ready {
					return nil, errors.Join(errCancelled, erase())
				}
			}
			n, err := p.in.Read(buf)
			if n == 0 && err != nil {
				if errors.Is(err, io.EOF) {
					err = errInputEnded
				}
				return nil, errors.Join(err, erase())
			}
			if n == 0 {
				return nil, errors.Join(errInputEnded, erase())
			}
			chunk = buf[:n]
		}
		picked, finished, err := s.keys(chunk)
		if finished {
			if eraseErr := erase(); eraseErr != nil {
				return nil, errors.Join(err, eraseErr)
			}
			return picked, err
		}
		if !s.pendingEscape() {
			escapeDeadline = time.Time{}
		} else if escapeDeadline.IsZero() {
			// A fixed 100 ms suffix window keeps standalone Escape responsive even if a prefix trickles in.
			escapeDeadline = time.Now().Add(100 * time.Millisecond)
		}
		if err := draw(); err != nil {
			return nil, err
		}
	}
}

// interruptible restores the terminal and exits 130 on SIGINT or SIGTERM until the returned stop runs. A signal
// ends the run before any answer is taken, so an interrupted question never counts as consent. stop waits for the
// watcher, so no goroutine outlives the question.
func (p terminalPrompter) interruptible(fd int, state *term.State) (stop func()) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case <-sig:
			restoreModes(fd, state)
			_, _ = fmt.Fprintln(p.stderr)
			os.Exit(130)
		case <-done:
		}
	}()
	return func() {
		signal.Stop(sig)
		close(done)
		<-finished
	}
}

// restoreModes puts back the modes saved before a question, including the kernel's own pending-input flag.
func restoreModes(fd int, state *term.State) {
	_ = term.Restore(fd, state)
	settle(fd)
}
