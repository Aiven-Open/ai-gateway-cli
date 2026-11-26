// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

// silence turns echo off before the prompt and leaves every other input mode as it was. term.MakeRaw would also set
// virtual-terminal input, which a legacy console refuses.
func silence(fd int) error {
	h := windows.Handle(fd)
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return err
	}
	return windows.SetConsoleMode(h, echoOff(mode))
}

// echoOff is mode without echo.
func echoOff(mode uint32) uint32 { return mode &^ windows.ENABLE_ECHO_INPUT }

// readHidden reads one line with term.ReadPassword, which clears echo and line input without virtual-terminal input.
func readHidden(fd int) ([]byte, error) { return term.ReadPassword(fd) }

func settle(int) {}
