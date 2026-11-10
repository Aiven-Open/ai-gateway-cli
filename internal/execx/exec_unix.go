// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build !windows

package execx

import "syscall"

// Exec replaces the launcher via execve and returns only on failure.
func (s Syscall) Exec(name string, argv, env []string) error {
	path, err := s.LookPath(name)
	if err != nil {
		return err
	}
	return syscall.Exec(path, argv, env)
}
