// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/execx"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
	"github.com/aiven-open/ai-gateway-cli/internal/gateway"
)

// Deps are the process resources commands use; tests substitute them.
type Deps struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// HTTP defaults to a client with the gateway timeout.
	HTTP *http.Client
	// Runner execs the harness a launch command prepared.
	Runner execx.Runner
	// Term describes the streams; its zero value is three non-terminals, as in tests and pipelines.
	Term Terminal
	// Prompter reads a key silently from a terminal; nil means read one line from Stdin.
	Prompter Prompter
	// Now defaults to time.Now.
	Now func() time.Time
	// WriteFile replaces the key file and tool configuration; nil means fsutil.WriteAtomic.
	WriteFile func(path string, data []byte, perm os.FileMode) error
	// Remove deletes the key file; nil means os.Remove.
	Remove func(path string) error
	// Build is the release metadata `--version` prints.
	Build Build
}

// Build is set by main from linker flags.
type Build struct{ Version, Commit, Date string }

// Prompter talks to an interactive terminal; nil in Deps means stdin is not one.
type Prompter interface {
	ReadSecret(prompt string) (string, error)
	// ReadLine shows prompt and returns the answer without its newline; io.EOF when input ended.
	ReadLine(prompt string) (string, error)
}

func (d Deps) gateway(baseURL string) gateway.Client {
	c := d.HTTP
	if c == nil {
		c = gateway.NewHTTPClient()
	}
	return gateway.Client{HTTP: c, BaseURL: baseURL}
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

func (d Deps) writeFile(path string, data []byte, perm os.FileMode) error {
	if d.WriteFile != nil {
		return d.WriteFile(path, data, perm)
	}
	return fsutil.WriteAtomic(path, data, perm)
}

// writeIn replaces name in dir once check passes; a WriteFile override gets the file's path instead.
func (d Deps) writeIn(dir *fsutil.Dir, name string, data []byte, perm os.FileMode, check func() error) error {
	if d.WriteFile == nil {
		return dir.WriteAtomic(name, data, perm, check)
	}
	if err := check(); err != nil {
		return err
	}
	return d.WriteFile(filepath.Join(dir.Path, name), data, perm)
}

func (d Deps) remove(path string) error {
	if d.Remove != nil {
		return d.Remove(path)
	}
	return os.Remove(path)
}
