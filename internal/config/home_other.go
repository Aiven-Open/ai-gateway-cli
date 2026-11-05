// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build !windows

package config

// HomeVar names the home directory.
const HomeVar = "HOME"

// SameVar reports whether two environment variable names refer to the same variable.
func SameVar(a, b string) bool { return a == b }
