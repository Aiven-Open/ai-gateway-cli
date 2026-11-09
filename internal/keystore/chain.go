// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package keystore

import (
	"errors"
	"fmt"
	"io"
)

// Chain stores in Primary (OS keychain) and falls back to Fallback (file) when Primary is unusable.
type Chain struct {
	Primary, Fallback Store
	// Notice receives the one-line message printed when a write falls back.
	Notice io.Writer
}

// Get serves the file record when one exists, else the keychain's. A file record means the keychain failed
// the last write (Set removes or overwrites it otherwise), and skipping the keychain avoids its timeout on every launch.
// Backend errors count as missing.
func (c *Chain) Get(baseURL string) (Record, error) {
	canon := Canonical(baseURL)
	for _, s := range []Store{c.Fallback, c.Primary} {
		if rec, err := s.Get(canon); err == nil && rec.BaseURL == canon {
			return rec, nil
		}
	}
	return Record{}, ErrNotFound
}

func (c *Chain) Set(rec Record) (string, error) {
	where, err := c.Primary.Set(rec)
	if err != nil {
		fmt.Fprintf(c.Notice, "aiven-llm: OS keychain unavailable (%v); storing the key in a file instead\n", err)
		return c.Fallback.Set(rec)
	}
	// Reads prefer the file, so a leftover must not outlive the new key: remove it, else overwrite it.
	if err := c.Fallback.Delete(rec.BaseURL); err != nil {
		if _, serr := c.Fallback.Set(rec); serr != nil {
			return "", fmt.Errorf("key stored in %s, but an older key file that reads prefer could not be removed or overwritten: %w", where, errors.Join(err, serr))
		}
		fmt.Fprintf(c.Notice, "aiven-llm: key stored, but an older key file could not be removed (%v)\n", err)
	}
	return where, nil
}

// Delete clears both backends. A keychain delete failure only warns when the keychain no longer serves the key:
// on hosts without one (headless Linux) its Get fails fast and the file is the store.
func (c *Chain) Delete(baseURL string) error {
	ferr := c.Fallback.Delete(baseURL)
	if err := c.Primary.Delete(baseURL); err != nil {
		if _, gerr := c.Primary.Get(Canonical(baseURL)); gerr == nil {
			return errors.Join(err, ferr)
		}
		fmt.Fprintf(c.Notice, "aiven-llm: could not remove the key from the OS keychain (%v)\n", err)
	}
	return ferr
}
