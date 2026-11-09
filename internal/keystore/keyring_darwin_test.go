// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build darwin

package keystore_test

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Aiven-Open/ai-gateway-cli/internal/keystore"
)

// Touches the real login keychain; opt in with AIVEN_LLM_IT_KEYCHAIN=1.
func TestKeyringAgainstRealMacOSKeychain(t *testing.T) {
	if os.Getenv("AIVEN_LLM_IT_KEYCHAIN") != "1" {
		t.Skip("set AIVEN_LLM_IT_KEYCHAIN=1 to use the real keychain")
	}
	k := keystore.NewKeyring()
	gwURL := fmt.Sprintf("https://it-%d.invalid", time.Now().UnixNano())
	t.Cleanup(func() { _ = k.Delete(gwURL) })
	if _, err := k.Set(keystore.Record{BaseURL: gwURL, Key: "it-key", WrittenAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if rec, err := k.Get(gwURL); err != nil || rec.Key != "it-key" {
		t.Fatalf("Get = %+v, %v", rec, err)
	}
	if err := k.Delete(gwURL); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Get(gwURL); !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("after Delete: err = %v, want ErrNotFound", err)
	}
}
