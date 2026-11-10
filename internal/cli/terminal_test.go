// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
)

// The terminal adapter needs a real terminal only for ReadSecret, so ReadLine is tested on a reader here.
func TestTerminalReadLineReportsEndOfInputAfterAPartialLine(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		eof      bool
	}{
		{"y\n", "y", false},
		{"n\r\n", "n", false},
		{"\n", "", false},
		{"y", "y", true},
		{" ", " ", true},
		{"", "", true},
	} {
		var prompt strings.Builder
		p := terminalPrompter{stderr: &prompt, lines: bufio.NewReader(strings.NewReader(tc.in))}
		got, err := p.ReadLine("? ")
		if got != tc.want || errors.Is(err, io.EOF) != tc.eof || prompt.String() != "? " {
			t.Errorf("ReadLine on %q = %q, %v (prompt %q); want %q, EOF %v", tc.in, got, err, prompt.String(), tc.want, tc.eof)
		}
		if _, err := confirm(p, "? ", true); tc.eof && err == nil {
			t.Errorf("confirm on %q after end of input: want error", tc.in)
		}
	}
}

func TestTerminalDoesNotReadInputWhenPromptCannotBeShown(t *testing.T) {
	t.Parallel()
	reader, writer := io.Pipe()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	p := terminalPrompter{stderr: writer}
	if value, err := p.ReadLine("? "); value != "" || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("value %q, error %v", value, err)
	}
}
