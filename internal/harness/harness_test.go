// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package harness_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/harness"
)

func TestModelArgsAddsTheModelOnlyWithoutNativeArgs(t *testing.T) {
	for _, tc := range []struct{ args, want []string }{
		{nil, []string{"--model", "m"}},
		{[]string{}, []string{"--model", "m"}},
		{[]string{"hi"}, []string{"hi"}},
		// A global option and its value ahead of a subcommand that takes no model flag.
		{[]string{"--log-level", "INFO", "models", "aiven"}, []string{"--log-level", "INFO", "models", "aiven"}},
		{[]string{"--", "--model"}, []string{"--", "--model"}},
	} {
		if got := harness.ModelArgs(tc.args, "m", "--model"); !slices.Equal(got, tc.want) {
			t.Errorf("ModelArgs(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestReadObjectAcceptsJSONCAndKeepsNumbersVerbatim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	src := "\xef\xbb\xbf{\n" +
		"  // line comment\r\n" +
		"  \"url\": \"http://host/*not a comment*/\", /* block */\n" +
		"  \"n\": 1.50,\n" +
		"  \"list\": [1, 2, /* last */ ],\n" +
		"  \"comma\": \"a,]\",\n" +
		"  \"dir\": \"C:\\\\\", // after an escaped backslash\n" +
		"  \"esc\": \"a\\\"//b\", // last\r\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	obj, err := harness.ReadObject(path)
	if err != nil {
		t.Fatal(err)
	}
	if obj["url"] != "http://host/*not a comment*/" || obj["n"] != json.Number("1.50") || obj["esc"] != `a"//b` ||
		obj["comma"] != "a,]" || obj["dir"] != `C:\` {
		t.Errorf("object = %v", obj)
	}
	if list, ok := obj["list"].([]any); !ok || len(list) != 2 {
		t.Errorf("list = %v, want two entries", obj["list"])
	}
}

func TestReadObjectTreatsAMissingFileAsEmpty(t *testing.T) {
	obj, err := harness.ReadObject(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil || obj == nil || len(obj) != 0 {
		t.Fatalf("ReadObject = %v, %v; want an empty object", obj, err)
	}
}

func TestReadObjectRefusesWhatIsNotOneObject(t *testing.T) {
	for name, src := range map[string]string{
		"array":                "[]",
		"null":                 "null",
		"two objects":          "{} {}",
		"unterminated comment": `{"a": 1} /* rest`,
		"comment joins tokens": `{"a": [1/**/2]}`,
		"data after a comment": `{"a": 1} // fine` + "\n" + `{"b": 2}`,
	} {
		path := filepath.Join(t.TempDir(), "f.json")
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.ReadObject(path); err == nil || !strings.Contains(err.Error(), "is not a JSON object") {
			t.Errorf("%s: err = %v, want refusal", name, err)
		}
	}
	if _, err := harness.ReadObject(t.TempDir()); err == nil || !strings.Contains(err.Error(), "is not a regular file") {
		t.Errorf("directory: err = %v, want a refusal", err)
	}
}

func TestObjectAddsAnAbsentObjectAndRefusesAnotherType(t *testing.T) {
	parent := map[string]any{"set": map[string]any{"a": 1}, "str": "x"}
	if got, err := harness.Object(parent, "set", "f"); err != nil || got["a"] != 1 {
		t.Errorf("existing = %v, %v", got, err)
	}
	got, err := harness.Object(parent, "new", "f")
	if err != nil {
		t.Fatal(err)
	}
	got["b"] = 2
	if inner, ok := parent["new"].(map[string]any); !ok || inner["b"] != 2 {
		t.Errorf("absent key not added to parent: %v", parent)
	}
	if _, err := harness.Object(parent, "str", "f"); err == nil || !strings.Contains(err.Error(), `f: "str" is not a JSON object`) {
		t.Errorf("string: err = %v", err)
	}
}

func TestJSONKeepsShellOperatorsReadable(t *testing.T) {
	got := string(harness.JSON(map[string]string{"helper": "[ -f k ] && cat k <in >out"}))
	if want := "{\n  \"helper\": \"[ -f k ] && cat k <in >out\"\n}\n"; got != want {
		t.Errorf("JSON = %q, want %q", got, want)
	}
}
