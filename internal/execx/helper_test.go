// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package execx_test

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/execx"
)

// TestMain turns the test binary into a portable native child fixture.
func TestMain(m *testing.M) {
	switch os.Getenv(modeVar) {
	case "exec", "exec-batch":
		target := os.Getenv("AIVEN_EXECX_BATCH_TARGET")
		argv := childArgv
		if os.Getenv(modeVar) == "exec-batch" {
			argv = batchChildArgv
		} else {
			var err error
			target, err = os.Executable()
			if err != nil {
				fmt.Fprintln(os.Stderr, "exec failed:", err)
				os.Exit(2)
			}
		}
		env := append([]string{}, childEnv...)
		if root := os.Getenv("SYSTEMROOT"); root != "" {
			env = append(env, "SYSTEMROOT="+root)
		}
		env = append(env, "AIVEN_EXECX_PARENT_PID="+strconv.Itoa(os.Getpid()))
		err := execx.Syscall{}.Exec(target, argv, env)
		fmt.Fprintln(os.Stderr, "exec failed:", err)
		os.Exit(2)
	case "print":
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(2)
		}
		parentPID, _ := strconv.Atoi(os.Getenv("AIVEN_EXECX_PARENT_PID"))
		fmt.Fprint(os.Stderr, "inherited stderr")
		if err := json.NewEncoder(os.Stdout).Encode(observed{
			Argv: os.Args, Env: os.Environ(), Input: string(input),
			PID: os.Getpid(), ParentPID: parentPID,
		}); err != nil {
			os.Exit(2)
		}
		os.Exit(23)
	case "exec-zero":
		exe, err := os.Executable()
		if err == nil {
			err = execx.Syscall{}.Exec(exe, []string{exe}, []string{modeVar + "=zero"})
		}
		fmt.Fprintln(os.Stderr, "exec returned:", err)
		os.Exit(2)
	case "exec-interrupt":
		exe, err := os.Executable()
		if err == nil {
			err = execx.Syscall{}.Exec(exe, []string{exe}, []string{modeVar + "=interrupt"})
		}
		fmt.Fprintln(os.Stderr, "exec returned:", err)
		os.Exit(2)
	case "interrupt":
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		fmt.Println("ready")
		<-sig
		os.Exit(23)
	case "zero":
		if _, err := fmt.Fprint(os.Stdout, "done"); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
