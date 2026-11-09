// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"
)

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
	fmt.Fprint(p.stderr, prompt)
	fd := int(p.in.Fd())
	// ReadPassword disables echo; a signal would otherwise leave the terminal silent.
	if state, err := term.GetState(fd); err == nil {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sig)
		go func() {
			if <-sig != nil {
				_ = term.Restore(fd, state)
				fmt.Fprintln(p.stderr)
				os.Exit(130)
			}
		}()
	}
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(p.stderr)
	return string(b), err
}

func (p terminalPrompter) ReadLine(prompt string) (string, error) {
	fmt.Fprint(p.stderr, prompt)
	line, err := p.lines.ReadString('\n')
	if err == io.EOF && line != "" {
		err = nil
	}
	return strings.TrimRight(line, "\r\n"), err
}
