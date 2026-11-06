// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build !darwin

package keystore

import "github.com/zalando/go-keyring"

// osKeychain is go-keyring as is: on Linux a Set blocked on the unlock prompt has not sent its write yet.
type osKeychain struct{}

func newOSKeychain() keychain { return osKeychain{} }

func (osKeychain) Get(service, user string) (string, error) { return keyring.Get(service, user) }
func (osKeychain) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}
func (osKeychain) Delete(service, user string) error { return keyring.Delete(service, user) }
