// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package execx hands control of the launcher to a harness.
package execx

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Runner launches a harness; argv[0] is name. Real implementations do not
// return on success: Unix replaces the process, Windows exits after waiting.
type Runner interface {
	// LookPath reports whether name is installed, before anything is fetched or written.
	LookPath(name string) (string, error)
	Exec(name string, argv, env []string) error
}

// Merge returns base with overrides laid over it: a name set in both takes the override, blank values included.
func Merge(base, overrides []string) []string {
	set := make(map[string]bool, len(overrides))
	for _, kv := range overrides {
		set[name(kv)] = true
	}
	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		if !set[name(kv)] {
			out = append(out, kv)
		}
	}
	return append(out, overrides...)
}

func name(kv string) string {
	n, _, _ := strings.Cut(kv, "=")
	if runtime.GOOS == "windows" {
		return strings.ToUpper(n)
	}
	return n
}

// Syscall finds a harness on PATH and hands control to it.
type Syscall struct{}

// LookPath finds name on PATH.
func (Syscall) LookPath(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s is required but not installed", name)
	}
	return path, nil
}
