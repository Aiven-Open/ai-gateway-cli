// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

//go:build darwin

package keystore_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/Aiven-Open/ai-gateway-cli/internal/keystore"
)

// Touches the real login keychain; opt in with AIVEN_LLM_IT_KEYCHAIN=1.
func TestKeyringAgainstRealMacOSKeychain(t *testing.T) {
	if os.Getenv("AIVEN_LLM_IT_KEYCHAIN") != "1" {
		t.Skip("set AIVEN_LLM_IT_KEYCHAIN=1 to use the real keychain")
	}
	// keyring.MockInit in other tests swaps go-keyring's backend for the whole process, so run alone in a fresh one.
	if os.Getenv("AIVEN_LLM_IT_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestKeyringAgainstRealMacOSKeychain$", "-test.count=1")
		cmd.Env = append(os.Environ(), "AIVEN_LLM_IT_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return
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
	// Entries go-keyring wrote before the launcher drove `security` itself still read back.
	data, _ := json.Marshal(keystore.Record{BaseURL: gwURL, Key: "old-key"})
	if err := keyring.Set("aiven-llm", gwURL, string(data)); err != nil {
		t.Fatal(err)
	}
	if rec, err := k.Get(gwURL); err != nil || rec.Key != "old-key" {
		t.Fatalf("Get after go-keyring Set = %+v, %v", rec, err)
	}
	if err := k.Delete(gwURL); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Get(gwURL); !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("after Delete: err = %v, want ErrNotFound", err)
	}
}
