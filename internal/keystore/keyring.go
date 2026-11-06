// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package keystore

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zalando/go-keyring"
)

const keyringService = "aiven-llm"

// keychainTimeout bounds each keychain call: a locked Secret Service can wait on an unlock prompt indefinitely.
const keychainTimeout = 5 * time.Second

var errKeychainTimeout = errors.New("keychain did not respond")

// keychain is the subset of go-keyring the backend uses.
type keychain interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

// selfBounded is a keychain that enforces the timeout itself and returns only once its call can no longer change state.
type selfBounded interface{ selfBounded() }

// goKeyring is go-keyring as is.
type goKeyring struct{}

func (goKeyring) Get(service, user string) (string, error) { return keyring.Get(service, user) }
func (goKeyring) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}
func (goKeyring) Delete(service, user string) error { return keyring.Delete(service, user) }

// Keyring keeps records in the OS keychain, one entry per canonical gateway URL.
type Keyring struct {
	kc      keychain
	timeout time.Duration
}

// NewKeyring returns the OS keychain backend.
func NewKeyring() *Keyring { return &Keyring{kc: newOSKeychain(), timeout: keychainTimeout} }

// bounded runs fn and gives up after the keyring's timeout; the abandoned call finishes in the background.
func bounded[T any](k *Keyring, fn func() (T, error)) (T, error) {
	if _, ok := k.kc.(selfBounded); ok {
		return fn()
	}
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() { v, err := fn(); ch <- result{v, err} }()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(k.timeout):
		var zero T
		return zero, errKeychainTimeout
	}
}

// Get reads the record for baseURL; a missing, corrupt or mismatched entry counts as missing.
func (k *Keyring) Get(baseURL string) (Record, error) {
	canon := Canonical(baseURL)
	data, err := bounded(k, func() (string, error) { return k.kc.Get(keyringService, canon) })
	if errors.Is(err, keyring.ErrNotFound) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("read keychain: %w", err)
	}
	var rec Record
	if json.Unmarshal([]byte(data), &rec) != nil || rec.BaseURL != canon || rec.Key == "" {
		return Record{}, ErrNotFound
	}
	return rec, nil
}

// Set stores rec and returns the location label for the login message.
func (k *Keyring) Set(rec Record) (string, error) {
	rec.BaseURL = Canonical(rec.BaseURL)
	data, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	_, err = bounded(k, func() (struct{}, error) { return struct{}{}, k.kc.Set(keyringService, rec.BaseURL, string(data)) })
	if err != nil {
		return "", fmt.Errorf("write keychain: %w", err)
	}
	return "OS keychain", nil
}

// Delete removes the entry; an absent one is not an error.
func (k *Keyring) Delete(baseURL string) error {
	canon := Canonical(baseURL)
	_, err := bounded(k, func() (struct{}, error) { return struct{}{}, k.kc.Delete(keyringService, canon) })
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("delete from keychain: %w", err)
	}
	return nil
}
