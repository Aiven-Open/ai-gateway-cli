// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"testing"

	"golang.org/x/sys/windows"
)

// A legacy console refuses virtual-terminal input, so hidden input must clear echo and set nothing.
func TestEchoOffLeavesVirtualTerminalInputAlone(t *testing.T) {
	const legacy = windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_INPUT
	if got, want := echoOff(
		legacy,
	), uint32(
		windows.ENABLE_LINE_INPUT|windows.ENABLE_PROCESSED_INPUT,
	); got != want {
		t.Errorf("echoOff(%#x) = %#x, want %#x", legacy, got, want)
	}
}
