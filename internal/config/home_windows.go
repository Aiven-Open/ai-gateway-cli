// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package config

import "strings"

// HomeVar names the home directory: USERPROFILE on Windows, as os.UserHomeDir and Node's os.homedir read it, so the
// launcher and the Node tools agree even where Git Bash sets a HOME of its own.
const HomeVar = "USERPROFILE"

// SameVar compares variable names as Windows does, ignoring case.
func SameVar(a, b string) bool { return strings.EqualFold(a, b) }
