// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build !windows

package cli

import (
	"io"
	"os"
)

func probeConsoleVT(*os.File) bool { return true }

func enableVT(*os.File, io.Writer) (func(), bool) { return func() {}, true }
