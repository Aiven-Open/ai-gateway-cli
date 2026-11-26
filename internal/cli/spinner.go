// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"io"
	"strings"
	"time"
)

const defaultSpinnerTick = 80 * time.Millisecond

// runWithProgress runs fn while animating a single-line spinner on u.w when u.color is true.
// In plain mode, NO_COLOR, TERM=dumb or on a non-TTY stream it executes fn directly with zero output.
// The spinner goroutine is synchronously joined and its line cleared (\r\x1b[2K) before returning.
func (u ui) runWithProgress(prefix, label string, fn func() error) error {
	return runWithProgressTick(u, prefix, label, defaultSpinnerTick, fn)
}

func runWithProgressTick(
	u ui,
	prefix, label string,
	tick time.Duration,
	fn func() error,
) error {
	if !u.color || u.w == nil {
		return fn()
	}
	if tick <= 0 {
		tick = defaultSpinnerTick
	}
	frames := u.th.g.spinner
	if len(frames) == 0 {
		frames = asciiGlyphs.spinner
	}
	cleanLabel := safe(label)
	stop := make(chan struct{})
	stopped := make(chan struct{})
	draw := func(frame string) {
		text := prefix + u.th.cyan(frame) + " " + cleanLabel
		if u.width > 2 {
			// Fit raw text conservatively so East Asian Ambiguous glyphs never soft-wrap the spinner line.
			budget := u.width - 2
			plainPrefix := stripSGR(prefix)
			rem := budget - cells(plainPrefix) - cells(frame) - 1
			if rem > 0 {
				text = prefix + u.th.cyan(frame) + " " + fitWith(cleanLabel, rem, u.th.g.ellipsis)
			} else {
				text = prefix + u.th.cyan(frame)
			}
		}
		_, _ = io.WriteString(u.w, "\r\x1b[2K"+text)
	}
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		i := 0
		draw(frames[i])
		for {
			select {
			case <-stop:
				_, _ = io.WriteString(u.w, "\r\x1b[2K")
				return
			case <-ticker.C:
				i = (i + 1) % len(frames)
				draw(frames[i])
			}
		}
	}()
	defer func() {
		close(stop)
		<-stopped
	}()
	return fn()
}

// stripSGR removes ANSI SGR escape sequences so cell-width budgeting measures only visible text.
func stripSGR(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == ';') {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				i = j
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
