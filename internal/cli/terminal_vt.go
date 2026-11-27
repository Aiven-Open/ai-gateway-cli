// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import "sync"

var (
	vtMu       sync.Mutex
	vtRestores []func()
)

// restoreAllVT restores every console mode enableOutputVT and enableVT changed, newest first, once. Launch calls it
// before the harness takes the console, since Windows Exec exits without returning; a signal exit calls it too.
func restoreAllVT() {
	vtMu.Lock()
	list := vtRestores
	vtRestores = nil
	vtMu.Unlock()
	for i := len(list) - 1; i >= 0; i-- {
		list[i]()
	}
}

func registerVTRestore(fn func()) func() {
	var once sync.Once
	wrapped := func() { once.Do(fn) }
	vtMu.Lock()
	vtRestores = append(vtRestores, wrapped)
	vtMu.Unlock()
	return wrapped
}
