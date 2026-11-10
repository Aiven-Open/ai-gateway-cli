// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import "golang.org/x/sys/unix"

const getTermios, setTermios = unix.TCGETS, unix.TCSETS

// settle has nothing to clear: Linux keeps no pending-input flag in the modes.
func settle(int) {}
