// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build darwin || linux

package cli

import (
	"bytes"
	"io"

	"golang.org/x/sys/unix"
)

// silence turns echo off in line mode, so the prompt can appear with echo already off. Raw mode would do for the
// echo, but the driver translates input as it arrives: an Enter typed or pasted under raw mode stays a carriage
// return, which line mode never takes as the end of the line.
func silence(fd int) error {
	tio, err := unix.IoctlGetTermios(fd, getTermios)
	if err != nil {
		return err
	}
	tio.Lflag = tio.Lflag&^unix.ECHO | unix.ICANON | unix.ISIG
	tio.Iflag |= unix.ICRNL
	return unix.IoctlSetTermios(fd, setTermios, tio)
}

// readHidden reads one line from the silenced terminal; the driver has already applied erase and kill. It replaces
// term.ReadPassword, which treats the empty read Ctrl-D produces as nothing and waits on, so it never reports end
// of input. Input ending before a newline is io.EOF, even after some characters: only Enter submits.
func readHidden(fd int) ([]byte, error) {
	var line []byte
	buf := make([]byte, 256)
	for {
		n, err := unix.Read(fd, buf)
		if err == unix.EINTR {
			continue
		} else if err != nil {
			return nil, err
		} else if n == 0 {
			return nil, io.EOF
		}
		line = append(line, buf[:n]...)
		if i := bytes.IndexByte(line, '\n'); i >= 0 {
			return line[:i], nil
		}
	}
}
