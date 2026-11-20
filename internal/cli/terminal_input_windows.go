// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build windows

package cli

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

// waitChoiceInput bounds a pending Escape on a Windows console input handle.
func waitChoiceInput(fd int, deadline time.Time) (bool, error) {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return false, nil
	}
	ms := uint32((remaining + time.Millisecond - 1) / time.Millisecond)
	event, err := windows.WaitForSingleObject(windows.Handle(fd), ms)
	switch event {
	case windows.WAIT_OBJECT_0:
		return true, nil
	case uint32(windows.WAIT_TIMEOUT):
		return false, nil
	default:
		if err == nil {
			err = fmt.Errorf("unexpected wait status %d", event)
		}
		return false, fmt.Errorf("wait for choice input: %w", err)
	}
}
