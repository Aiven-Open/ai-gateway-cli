// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"fmt"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
)

// storedKey returns the key the tools configured for env's gateway read, or "" when there is none: that gateway's key
// file. A key saved for another gateway is in another file, and the environment's names no gateway, so neither is read.
func storedKey(env config.Env) (string, error) {
	return creds.Load(env.UserHome, env.BaseURL)
}

// saveKey writes key, already checked against env's gateway, to that gateway's key file, replacing it in one rename.
func saveKey(env config.Env, deps Deps, key string) (string, error) {
	path, err := creds.Write(env.UserHome, env.BaseURL, key, deps.writeFile)
	if err != nil {
		return "", fmt.Errorf("key not saved: %w", err)
	}
	return path, nil
}

// removeKey deletes env's gateway's key file, leaving other gateways' key files alone.
func removeKey(env config.Env, deps Deps) error {
	return creds.Delete(env.UserHome, env.BaseURL, deps.remove)
}
