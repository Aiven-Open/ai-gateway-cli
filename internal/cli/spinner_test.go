// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestSpinnerNoOpInPlainAndNonTTY(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		tty   bool
		plain bool
	}{
		{name: "non-tty", tty: false, plain: false},
		{name: "plain on tty", tty: true, plain: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf syncBuffer
			u := newUI(&buf, tc.tty, func() int { return 80 }, tc.plain)
			called := false
			err := u.runWithProgress("", "Checking models", func() error {
				called = true
				return nil
			})
			if err != nil || !called {
				t.Fatalf("err=%v called=%v", err, called)
			}
			if buf.String() != "" {
				t.Fatalf("plain/non-tty spinner wrote %q, want empty", buf.String())
			}
		})
	}
}

func TestSpinnerSynchronouslyClearsAndStopsBeforeReturn(t *testing.T) {
	t.Parallel()
	var buf syncBuffer
	u := newUI(&buf, true, func() int { return 80 }, false)
	wantErr := errors.New("boom")
	gotErr := runWithProgressTick(u, "│  ", "Validating key", 2*time.Millisecond, func() error {
		time.Sleep(15 * time.Millisecond)
		return wantErr
	})
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("err = %v, want %v", gotErr, wantErr)
	}
	// Immediately write a durable line after return: must not race under -race or interleave with spinner.
	u.line(0, "durable line")
	out := buf.String()
	if !strings.Contains(out, "Validating key") {
		t.Errorf("spinner never drew label: %q", out)
	}
	if !strings.HasSuffix(out, "\r\x1b[2Kdurable line\n") {
		t.Errorf("spinner did not clear line before durable output: %q", out)
	}
	// Must never hide the cursor.
	if strings.Contains(out, "\x1b[?25l") {
		t.Errorf("spinner hid cursor: %q", out)
	}
}

func TestThemePlainAndNonUTF8Fallback(t *testing.T) {
	t.Parallel()
	plain := newTheme(false, true)
	if got := plain.bold("hello"); got != "hello" {
		t.Errorf("plain bold = %q, want hello", got)
	}
	if got := plain.cyan("x"); got != "x" {
		t.Errorf("plain cyan = %q, want x", got)
	}
	if plain.g.railBar != "|" || plain.g.cursor != ">" {
		t.Errorf("plain glyphs not ASCII: %+v", plain.g)
	}

	asciiTTY := newTheme(true, utf8Locale([]string{"LC_ALL=C"}))
	if asciiTTY.g.railBar != "|" || asciiTTY.g.cursor != ">" || asciiTTY.g.ok != "+" {
		t.Errorf("LC_ALL=C glyphs = %+v, want ASCII", asciiTTY.g)
	}
	if !strings.Contains(asciiTTY.green("ok"), "\x1b[32m") {
		t.Errorf("asciiTTY green = %q", asciiTTY.green("ok"))
	}

	utf8TTY := newTheme(true, utf8Locale([]string{"LANG=en_US.UTF-8"}))
	if utf8TTY.g.railBar != "│" || utf8TTY.g.cursor != "❯" || utf8TTY.g.ok != "✓" {
		t.Errorf("UTF-8 glyphs = %+v", utf8TTY.g)
	}
	if utf8Locale([]string{"LC_ALL=POSIX"}) {
		t.Error("LC_ALL=POSIX reported UTF-8")
	}
}
