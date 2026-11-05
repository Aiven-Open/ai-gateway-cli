// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build !darwin && !linux

package cli

import (
	"fmt"
	"runtime"
	"time"
)

func waitChoiceInput(int, time.Time) (bool, error) {
	return false, fmt.Errorf("bounded inline Escape input is unsupported on %s; use --plain", runtime.GOOS)
}
