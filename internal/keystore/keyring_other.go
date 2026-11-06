// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build !darwin

package keystore

// Linux keeps go-keyring: a Set blocked on the unlock prompt has not sent its write yet, so an abandoned call cannot land.
var newOSKeychain = func() keychain { return goKeyring{} }
