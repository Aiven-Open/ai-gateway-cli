// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build windows

package cli

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

// probeConsoleVT checks whether Windows virtual terminal input and output modes can be enabled on in and errOut
// without permanently mutating console modes (or touching stdout). Returns false on legacy ConHost consoles so the
// presenter falls back to plain mode automatically.
func probeOutputVT(out *os.File) bool {
	if out == nil || !term.IsTerminal(int(out.Fd())) {
		return true
	}
	h := windows.Handle(out.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING == 0 {
		if err := windows.SetConsoleMode(
			h,
			mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING,
		); err != nil {
			return false
		}
		_ = windows.SetConsoleMode(h, mode)
	}
	return true
}

func probeConsoleVT(in, errOut *os.File) bool {
	if errOut != nil && term.IsTerminal(int(errOut.Fd())) {
		h := windows.Handle(errOut.Fd())
		var mode uint32
		if err := windows.GetConsoleMode(h, &mode); err != nil {
			return false
		}
		if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING == 0 {
			if err := windows.SetConsoleMode(
				h,
				mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING,
			); err != nil {
				return false
			}
			_ = windows.SetConsoleMode(h, mode)
		}
	}
	if in != nil && term.IsTerminal(int(in.Fd())) {
		h := windows.Handle(in.Fd())
		var mode uint32
		if err := windows.GetConsoleMode(h, &mode); err != nil {
			return false
		}
		if mode&windows.ENABLE_VIRTUAL_TERMINAL_INPUT == 0 {
			if err := windows.SetConsoleMode(
				h,
				mode|windows.ENABLE_VIRTUAL_TERMINAL_INPUT,
			); err != nil {
				return false
			}
			_ = windows.SetConsoleMode(h, mode)
		}
	}
	return true
}

// enableOutputVT enables ENABLE_VIRTUAL_TERMINAL_PROCESSING on any TTY *os.File streams for the duration of a
// command, returning a restore function that resets original console modes on exit.
func enableOutputVT(streams ...io.Writer) func() {
	type saved struct {
		h    windows.Handle
		mode uint32
	}
	var list []saved
	for _, w := range streams {
		f, ok := w.(*os.File)
		if !ok || f == nil || !term.IsTerminal(int(f.Fd())) {
			continue
		}
		h := windows.Handle(f.Fd())
		var mode uint32
		if err := windows.GetConsoleMode(h, &mode); err != nil {
			continue
		}
		if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
			continue
		}
		if err := windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
			continue
		}
		list = append(list, saved{h: h, mode: mode})
	}
	return registerVTRestore(func() {
		for i := len(list) - 1; i >= 0; i-- {
			_ = windows.SetConsoleMode(list[i].h, list[i].mode)
		}
	})
}

// enableVT turns on virtual-terminal output on stderr and virtual-terminal input on stdin for inline selection on
// Windows, returning a restore callback. If either fails, it restores prior modes and returns ok=false.
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
	return registerVTRestore(func() {
		_ = windows.SetConsoleMode(inHandle, inMode)
		_ = windows.SetConsoleMode(outHandle, outMode)
	}), true
}
