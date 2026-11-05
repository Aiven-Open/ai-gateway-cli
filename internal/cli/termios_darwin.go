// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import "golang.org/x/sys/unix"

const getTermios, setTermios = unix.TIOCGETA, unix.TIOCSETA

// fionread is _IOR('f', 127, int) from sys/filio.h; x/sys/unix does not define it for darwin.
const fionread = 0x4004667f

// settle clears PENDIN, which xnu sets whenever a mode change turns canonical input back on, so the restored modes
// equal the saved ones. Asking how much input is waiting has the kernel retype pending input and clear the flag,
// without taking any typed-ahead input.
func settle(fd int) {
	_, _ = unix.IoctlGetInt(fd, fionread)
}
