// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build windows

package cli

import (
	"encoding/binary"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procPeekConsoleInputW = kernel32.NewProc("PeekConsoleInputW")
	procReadConsoleInputW = kernel32.NewProc("ReadConsoleInputW")
)

const keyEventType = 0x0001

// inputRecord matches the Win32 INPUT_RECORD layout (20 bytes).
type inputRecord struct {
	eventType uint16
	_         uint16
	event     [16]byte
}

func isCharKeyDown(rec inputRecord) bool {
	if rec.eventType != keyEventType {
		return false
	}
	keyDown := binary.LittleEndian.Uint32(rec.event[0:4])
	unicodeChar := binary.LittleEndian.Uint16(rec.event[10:12])
	return keyDown != 0 && unicodeChar != 0
}

// waitChoiceInput bounds a pending Escape on a Windows console input handle, discarding non-character and key-up
// console records (mouse, resize, focus) so ReadConsole does not block past deadline.
func waitChoiceInput(fd int, deadline time.Time) (bool, error) {
	h := windows.Handle(fd)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, nil
		}
		ms := uint32((remaining + time.Millisecond - 1) / time.Millisecond)
		event, err := windows.WaitForSingleObject(h, ms)
		switch event {
		case uint32(windows.WAIT_TIMEOUT):
			return false, nil
		case windows.WAIT_OBJECT_0:
			var rec inputRecord
			var count uint32
			r1, _, callErr := procPeekConsoleInputW.Call(
				uintptr(h),
				uintptr(unsafe.Pointer(&rec)),
				1,
				uintptr(unsafe.Pointer(&count)),
			)
			if r1 == 0 {
				return false, fmt.Errorf("peek choice input: %w", callErr)
			}
			if count == 0 {
				continue
			}
			if isCharKeyDown(rec) {
				return true, nil
			}
			// Discard non-character / key-up record so WaitForSingleObject does not spin on it.
			r1, _, callErr = procReadConsoleInputW.Call(
				uintptr(h),
				uintptr(unsafe.Pointer(&rec)),
				1,
				uintptr(unsafe.Pointer(&count)),
			)
			if r1 == 0 {
				return false, fmt.Errorf("discard non-key console event: %w", callErr)
			}
		default:
			if err == nil {
				err = fmt.Errorf("unexpected wait status %d", event)
			}
			return false, fmt.Errorf("wait for choice input: %w", err)
		}
	}
}
