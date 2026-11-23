// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build !windows

package cli

import (
	"io"
	"os"
)

func probeConsoleVT(*os.File, *os.File) bool { return true }

func probeOutputVT(*os.File) bool { return true }

func enableOutputVT(...io.Writer) func() { return func() {} }

func enableVT(*os.File, io.Writer) (func(), bool) { return func() {}, true }

func restoreAllVT() {}
