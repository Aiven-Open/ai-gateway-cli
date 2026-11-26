// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package fsutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrLocked means another holder kept the lock for the whole wait.
var ErrLocked = errors.New("held by another process")

// lockPoll is how often Lock tries again while the lock is held.
const lockPoll = 50 * time.Millisecond

// Lock takes an exclusive advisory lock on name in d, creating the file 0600, waiting up to wait
// while another open of it holds the lock. The lock lasts until the returned func runs or the
// process exits.
func (d *Dir) Lock(name string, wait time.Duration) (func(), error) {
	path := filepath.Join(d.Path, name)
	f, err := d.root.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	deadline := time.Now().Add(wait)
	for {
		held, err := tryLock(f)
		switch {
		case err != nil:
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		case held:
			return func() { _ = unlock(f); _ = f.Close() }, nil
		case !time.Now().Before(deadline):
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, ErrLocked)
		}
		time.Sleep(lockPoll)
	}
}
