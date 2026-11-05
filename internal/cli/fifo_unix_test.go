// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build unix

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A configuration file that is a FIFO, or a symlink to one, is refused before the gateway is contacted, instead of
// blocking setup or launch on a read that waits for a writer.
func TestSetupAndLaunchRefuseAFIFOWithoutBlocking(t *testing.T) {
	t.Parallel()
	for tool, file := range toolFiles {
		for _, kind := range []string{"fifo", "symlink"} {
			for _, command := range []string{"setup", "launch"} {
				t.Run(tool+" "+kind+" "+command, func(t *testing.T) {
					t.Parallel()
					g := newGatewayDouble(t)
					s := newSession(t, g).stdin(keyValid)
					path := filepath.Join(s.fakeHome, file)
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
					fifo, want := path, "not a regular file"
					if kind == "symlink" {
						fifo, want = filepath.Join(t.TempDir(), "fifo"), "is a symlink"
						if err := os.Symlink(fifo, path); err != nil {
							t.Fatal(err)
						}
					}
					if err := syscall.Mkfifo(fifo, 0o600); err != nil {
						t.Fatal(err)
					}
					args := []string{"setup", tool, "--key-stdin", "--yes"}
					if command == "launch" {
						args = []string{"launch", tool}
					}
					done := make(chan result, 1)
					go func() { done <- s.run(args...) }()
					select {
					case r := <-done:
						if r.code != 1 || !strings.Contains(r.stderr, want) || len(g.journal()) != 0 {
							t.Errorf("exit %d, requests %d, want %q: %s", r.code, len(g.journal()), want, r.stderr)
						}
					case <-time.After(10 * time.Second):
						t.Fatal("blocked on the FIFO")
					}
				})
			}
		}
	}
}
