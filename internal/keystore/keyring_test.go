// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package keystore_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/Aiven-Open/ai-gateway-cli/internal/keystore"
)

func TestKeyringRoundTripsRecordWithOptionalFields(t *testing.T) {
	keyring.MockInit()
	k := keystore.NewKeyring()
	want := keystore.Record{
		BaseURL: "https://ai.aiven.io", Key: "k1", KeyID: "kid", Project: "proj",
		APIURL: "https://api.aiven.io", WrittenAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}
	if _, err := k.Set(want); err != nil {
		t.Fatal(err)
	}
	got, err := k.Get("HTTPS://ai.aiven.io/")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Get = %+v, %v; want %+v", got, err, want)
	}
}
