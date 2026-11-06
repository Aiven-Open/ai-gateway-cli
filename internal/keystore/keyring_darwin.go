// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build darwin

package keystore

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

// go-keyring v0.2.8's prefixes, so entries it stored still read back.
const (
	base64Prefix = "go-keyring-base64:"
	hexPrefix    = "go-keyring-encoded:"
)

// macKeychain drives `security` itself and kills it at the deadline: go-keyring leaves the child running past a timeout,
// so a timed-out write could land after a newer login.
type macKeychain struct {
	path    string
	timeout time.Duration
}

func newOSKeychain() keychain {
	return macKeychain{path: "/usr/bin/security", timeout: keychainTimeout}
}

func (k macKeychain) Get(service, user string) (string, error) {
	out, err := k.run("", "find-generic-password", "-s", service, "-wa", user)
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if v, ok := strings.CutPrefix(out, base64Prefix); ok {
		b, err := base64.StdEncoding.DecodeString(v)
		return string(b), err
	}
	if v, ok := strings.CutPrefix(out, hexPrefix); ok {
		b, err := hex.DecodeString(v)
		return string(b), err
	}
	return out, nil
}

// Set passes the secret on stdin (`security -i`), never in argv.
func (k macKeychain) Set(service, user, password string) error {
	cmd := fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n", quote(service), quote(user), quote(base64Prefix+base64.StdEncoding.EncodeToString([]byte(password))))
	_, err := k.run(cmd, "-i")
	return err
}

func (k macKeychain) Delete(service, user string) error {
	_, err := k.run("", "delete-generic-password", "-s", service, "-a", user)
	return err
}

func (k macKeychain) run(stdin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), k.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, k.path, args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.CombinedOutput()
	if strings.Contains(string(out), "could not be found") {
		return "", keyring.ErrNotFound
	}
	if ctx.Err() != nil {
		return "", errKeychainTimeout
	}
	return string(out), err
}

// quote single-quotes s for the `security -i` command line, as go-keyring's shellescape does.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }
