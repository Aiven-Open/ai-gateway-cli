// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package execx

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
)

// Exec waits for a child with inherited streams, then exits with its status.
// Windows has no execve; returning success would resume the launcher's workflow.
func (s Syscall) Exec(name string, argv, env []string) error {
	path, err := s.LookPath(name)
	if err != nil {
		return err
	}
	if len(argv) == 0 {
		return errors.New("cannot launch a harness without argv")
	}
	cmd := exec.Command(path)
	cmd.Args = argv
	if isBatch(path) {
		comspec := cmdExe()
		line, err := BatchCmdLine(comspec, path, argv[1:])
		if err != nil {
			return fmt.Errorf("start harness: %w", err)
		}
		cmd = exec.Command(comspec)
		cmd.Args = nil
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
	}
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Ctrl-C reaches every process on the console. Unless SIGINT is notified,
	// the runtime defers to the default handler, which kills the launcher while
	// the harness runs on; signal.Ignore does not prevent that on Windows.
	signal.Notify(make(chan os.Signal, 1), os.Interrupt)
	err = cmd.Run()
	if err == nil {
		os.Exit(0)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	return fmt.Errorf("start harness: %w", err)
}

func cmdExe() string {
	if root := os.Getenv("SystemRoot"); root != "" {
		return filepath.Join(root, "System32", "cmd.exe")
	}
	if path, err := exec.LookPath("cmd.exe"); err == nil {
		return path
	}
	return `C:\Windows\System32\cmd.exe`
}
