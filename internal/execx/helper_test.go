// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package execx_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/Aiven-Open/ai-gateway-cli/internal/execx"
)

// TestMain turns the test binary into a helper process: "exec" execs itself via
// Syscall.Exec, "print" reports the argv and env it was started with.
func TestMain(m *testing.M) {
	switch os.Getenv(modeVar) {
	case "exec":
		exe, err := os.Executable()
		if err == nil {
			err = execx.Syscall{}.Exec(exe, childArgv, childEnv)
		}
		fmt.Fprintln(os.Stderr, "exec failed:", err)
		os.Exit(2)
	case "print":
		if err := json.NewEncoder(os.Stdout).Encode(observed{Argv: os.Args, Env: os.Environ()}); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
