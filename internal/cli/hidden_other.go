// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build !darwin && !linux

package cli

import "golang.org/x/term"

// silence turns echo off before the prompt with raw mode. Whether a key typed before term.ReadPassword sets its own
// mode survives on these platforms is unverified; hidden_unix.go explains the hazard.
func silence(fd int) error {
	_, err := term.MakeRaw(fd)
	return err
}

func readHidden(fd int) ([]byte, error) { return term.ReadPassword(fd) }

func settle(int) {}
