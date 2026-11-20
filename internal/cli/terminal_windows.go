// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build windows

package cli

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

// probeConsoleVT enables ENABLE_VIRTUAL_TERMINAL_PROCESSING on f when it is a Windows console so ANSI-16 SGR codes
// render cleanly, returning false on legacy ConHost consoles where VT processing is unavailable.
func probeConsoleVT(f *os.File) bool {
	if f == nil || !term.IsTerminal(int(f.Fd())) {
		return true
	}
	h := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}

// enableVT turns on virtual-terminal output on stderr and virtual-terminal input on stdin for inline selection on
// Windows, returning a restore callback. If either fails (legacy ConHost), it restores prior modes and returns ok=false.
func enableVT(in *os.File, stderr io.Writer) (restore func(), ok bool) {
	errFile, isFile := stderr.(*os.File)
	if !isFile || in == nil {
		return func() {}, false
	}
	outHandle := windows.Handle(errFile.Fd())
	var outMode uint32
	if err := windows.GetConsoleMode(outHandle, &outMode); err != nil {
		return func() {}, false
	}
	if err := windows.SetConsoleMode(
		outHandle,
		outMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING,
	); err != nil {
		return func() {}, false
	}
	inHandle := windows.Handle(in.Fd())
	var inMode uint32
	if err := windows.GetConsoleMode(inHandle, &inMode); err != nil {
		_ = windows.SetConsoleMode(outHandle, outMode)
		return func() {}, false
	}
	if err := windows.SetConsoleMode(
		inHandle,
		inMode|windows.ENABLE_VIRTUAL_TERMINAL_INPUT,
	); err != nil {
		_ = windows.SetConsoleMode(outHandle, outMode)
		return func() {}, false
	}
	return func() {
		_ = windows.SetConsoleMode(inHandle, inMode)
		_ = windows.SetConsoleMode(outHandle, outMode)
	}, true
}
