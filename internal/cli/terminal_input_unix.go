// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build darwin || linux

package cli

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// waitChoiceInput bounds a pending Escape without changing descriptor flags or owning another reader.
func waitChoiceInput(fd int, deadline time.Time) (bool, error) {
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, nil
		}
		n, err := unix.Poll(poll, int((remaining+time.Millisecond-1)/time.Millisecond))
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("wait for choice input: %w", err)
		}
		if poll[0].Revents&unix.POLLNVAL != 0 {
			return false, fmt.Errorf("wait for choice input: %w", unix.EBADF)
		}
		return n > 0, nil
	}
}
