// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package keystore

import (
	"testing"

	"github.com/zalando/go-keyring"
)

// MockOSKeychain points NewKeyring at go-keyring's in-memory mock; the macOS backend would otherwise reach the real keychain.
func MockOSKeychain(t testing.TB) {
	keyring.MockInit()
	old := newOSKeychain
	newOSKeychain = func() keychain { return goKeyring{} }
	t.Cleanup(func() { newOSKeychain = old })
}
