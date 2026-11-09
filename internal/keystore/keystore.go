// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package keystore persists gateway keys, one record per canonical gateway URL.
package keystore

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// ErrNotFound means no usable record exists for the gateway URL.
var ErrNotFound = errors.New("no key stored")

// Record is the stored value. KeyID, Project and APIURL are optional metadata; the launcher reads only Key.
type Record struct {
	BaseURL   string    `json:"base_url"`
	Key       string    `json:"key"`
	KeyID     string    `json:"key_id,omitempty"`
	Project   string    `json:"project,omitempty"`
	APIURL    string    `json:"api_url,omitempty"`
	WrittenAt time.Time `json:"written_at"`
}

// Store is one key backend. Set returns a human-readable location for the confirmation message.
type Store interface {
	Get(baseURL string) (Record, error)
	Set(rec Record) (location string, err error)
	Delete(baseURL string) error
}

// Canonical reduces a gateway URL to lowercase scheme://host[:port] with no path or trailing slash.
func Canonical(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.ToLower(strings.TrimRight(raw, "/"))
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}
