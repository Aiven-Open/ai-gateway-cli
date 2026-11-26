// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"errors"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

// errLockBusy blames the other run, not the files, when the lock stays held past the wait.
var errLockBusy = errors.New(
	"another aiven-ai-gateway run is changing the saved key; retry when it finishes",
)

// lockState holds config.LockFile until the returned func runs.
func lockState(env config.Env, deps Deps) (func(), error) {
	unlock, err := config.LockState(env.UserHome, deps.lockWait())
	if errors.Is(err, fsutil.ErrLocked) {
		return nil, errLockBusy
	}
	return unlock, err
}
