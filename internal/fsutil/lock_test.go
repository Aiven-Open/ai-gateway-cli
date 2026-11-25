// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package fsutil_test

import (
	"errors"
	"testing"
	"time"

	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

// Two opens of the lock file conflict even within one process, as they do between processes.
func TestLockExcludesSecondHolder(t *testing.T) {
	dir, err := fsutil.OpenDir(t.TempDir(), ".")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Close() }()
	unlock, err := dir.Lock("state.lock", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := dir.Lock("state.lock", 100*time.Millisecond); !errors.Is(err, fsutil.ErrLocked) {
		t.Fatalf("second Lock = %v, want ErrLocked", err)
	}
	if waited := time.Since(start); waited < 100*time.Millisecond || waited > 2*time.Second {
		t.Errorf("second Lock gave up after %v, want about 100ms", waited)
	}
	unlock()
	again, err := dir.Lock("state.lock", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Lock after release: %v", err)
	}
	again()
}
