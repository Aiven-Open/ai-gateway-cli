// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package harness_test

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/harness"
)

func writeTemp(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f.json")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// edit decodes src, applies change to the result and edits src to match it.
func edit(t *testing.T, src string, change func(obj map[string]any)) (string, error) {
	t.Helper()
	obj := map[string]any{}
	if strings.TrimSpace(src) != "" {
		_, decoded, err := harness.ReadJSON(writeTemp(t, src))
		if err != nil {
			t.Fatal(err)
		}
		obj = decoded
	}
	change(obj)
	out, err := harness.Edit("f.json", []byte(src), obj)
	return string(out), err
}

func TestEditRewritesOnlyWhatChanges(t *testing.T) {
	const src = "\xef\xbb\xbf// Pi models\n{\n\t\"providers\": {\n\t\t// mine\n\t\t\"local\": {\"baseUrl\": \"http://localhost\", \"n\": 1.0},\n\t\t\"aiven\": {\"api\": \"old\"},\n\t},\n\t\"theme\": \"dark\", // keep\n}\n"
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
		want   string
	}{
		{"nothing", func(map[string]any) {}, src},
		{"a nested value", func(o map[string]any) {
			o["providers"].(map[string]any)["aiven"].(map[string]any)["api"] = "new"
		}, strings.Replace(src, `"old"`, `"new"`, 1)},
		{"a new member", func(o map[string]any) {
			o["providers"].(map[string]any)["aiven"] = map[string]any{"api": "new", "models": []string{"m"}}
			o["defaultProvider"] = "aiven"
		}, "\xef\xbb\xbf// Pi models\n{\n\t\"providers\": {\n\t\t// mine\n\t\t\"local\": {\"baseUrl\": \"http://localhost\", \"n\": 1.0},\n\t\t\"aiven\": {\"api\": \"new\", \"models\": [\"m\"]},\n\t},\n\t\"theme\": \"dark\", // keep\n\t\"defaultProvider\": \"aiven\",\n}\n"},
		{"a replaced object", func(o map[string]any) {
			o["providers"].(map[string]any)["aiven"] = []string{"x"}
		}, strings.Replace(src, `{"api": "old"}`, "[\n\t\t\t\"x\"\n\t\t]", 1)},
		{"the first and last members", func(o map[string]any) {
			delete(o["providers"].(map[string]any), "local")
			delete(o, "theme")
		}, "\xef\xbb\xbf// Pi models\n{\n\t\"providers\": {\n\t\t\"aiven\": {\"api\": \"old\"},\n\t},\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := edit(t, src, tc.change)
			if err != nil || got != tc.want {
				t.Errorf("Edit = %v\n%s\nwant\n%s", err, got, tc.want)
			}
		})
	}
}

func TestEditKeepsCommasValid(t *testing.T) {
	for _, tc := range []struct {
		src    string
		change func(map[string]any)
		want   string
	}{
		{`{"a": 1, "b": 2}`, func(o map[string]any) { delete(o, "b") }, `{"a": 1}`},
		{`{"a": 1, "b": 2}`, func(o map[string]any) { delete(o, "a") }, `{"b": 2}`},
		{`{"a": 1, "b": 2, "c": 3}`, func(o map[string]any) { delete(o, "b") }, `{"a": 1, "c": 3}`},
		{`{"a": 1}`, func(o map[string]any) { o["b"] = 2 }, `{"a": 1, "b": 2}`},
		{"{\n  \"a\": 1\n}", func(o map[string]any) { o["b"] = map[string]any{"c": true} }, "{\n  \"a\": 1,\n  \"b\": {\n    \"c\": true\n  }\n}"},
		{"{\n  \"a\": 1,\n}", func(o map[string]any) { o["b"] = 2 }, "{\n  \"a\": 1,\n  \"b\": 2,\n}"},
		{"{\n  \"a\": 1,\n  \"b\": 2\n}", func(o map[string]any) { delete(o, "b"); o["c"] = 3 }, "{\n  \"a\": 1,\n  \"c\": 3\n}"},
		{`{"a": 1}`, func(o map[string]any) { delete(o, "a") }, `{}`},
		{`{"a": 1}`, func(o map[string]any) { o["a"] = "<&>" }, `{"a": "<&>"}`},
		{"{}", func(o map[string]any) { o["a"] = 1; o["b"] = 2 }, "{\n  \"a\": 1,\n  \"b\": 2\n}"},
		{"", func(o map[string]any) { o["a"] = 1 }, "{\n  \"a\": 1\n}\n"},
	} {
		// A comment keeps a one-line file from being rewritten whole.
		src, want := tc.src, tc.want
		if src != "" && !strings.Contains(src, "\n") {
			src, want = "/**/"+src, "/**/"+want
		}
		got, err := edit(t, src, tc.change)
		if err != nil || got != want {
			t.Errorf("Edit(%q) = %q, %v; want %q", src, got, err, want)
		}
	}
}

func TestEditKeepsLinesWhereMembersShareThem(t *testing.T) {
	const shared = "{\n  \"a\": 1, \"b\": 2\n}\n"
	const spanning = "{\n  \"a\": 1, /* x\n  */ \"b\": 2\n}\n"
	for _, tc := range []struct {
		name, src string
		change    func(map[string]any)
		want      string
	}{
		{"first removed", shared, func(o map[string]any) { delete(o, "a") }, "{\n  \"b\": 2\n}\n"},
		{"last removed", shared, func(o map[string]any) { delete(o, "b") }, "{\n  \"a\": 1\n}\n"},
		{"last replaced", shared, func(o map[string]any) { delete(o, "b"); o["c"] = 3 }, "{\n  \"a\": 1,\n  \"c\": 3\n}\n"},
		{"comment spanning lines, first removed", spanning, func(o map[string]any) { delete(o, "a") }, "{\n  /* x\n  */ \"b\": 2\n}\n"},
		{"comment spanning lines, last removed", spanning, func(o map[string]any) { delete(o, "b") }, "{\n  \"a\": 1\n}\n"},
		{"inline object replaced", "{\n  \"x\": {\"a\": 1}\n}\n", func(o map[string]any) { o["x"] = map[string]any{"b": 2} },
			"{\n  \"x\": {\n    \"b\": 2\n  }\n}\n"},
		{"CRLF", "{\r\n  \"a\": 1\r\n}\r\n", func(o map[string]any) { o["b"] = map[string]any{"c": 2} },
			"{\r\n  \"a\": 1,\r\n  \"b\": {\r\n    \"c\": 2\r\n  }\r\n}\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := edit(t, tc.src, tc.change)
			if err != nil || got != tc.want {
				t.Errorf("Edit(%q) = %q, %v; want %q", tc.src, got, err, tc.want)
			}
		})
	}
}

// Comments in an empty object belong to no member, so the first members added go after them.
func TestEditKeepsCommentsInAnEmptyObject(t *testing.T) {
	for _, tc := range []struct {
		src    string
		change func(map[string]any)
		want   string
	}{
		{"{\n  // root\n}\n", func(o map[string]any) { o["a"] = 1 }, "{\n  // root\n  \"a\": 1\n}\n"},
		{"{ /* none yet */ }", func(o map[string]any) { o["a"] = 1 }, "{ /* none yet */\n  \"a\": 1\n}"},
		{"{\r\n  \"providers\": { // add yours\r\n  }\r\n}\r\n", func(o map[string]any) {
			o["providers"] = map[string]any{"aiven": 1}
		}, "{\r\n  \"providers\": { // add yours\r\n    \"aiven\": 1\r\n  }\r\n}\r\n"},
	} {
		got, err := edit(t, tc.src, tc.change)
		if err != nil || got != tc.want {
			t.Errorf("Edit(%q) = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
}

func TestEditRewritesAOneLineFileWithoutCommentsWhole(t *testing.T) {
	for _, src := range []string{`{"b":{"c":2},"a":1}`, "\xef\xbb\xbf{\"b\":{\"c\":2},\"a\":1}\n"} {
		got, err := edit(t, src, func(o map[string]any) { o["b"].(map[string]any)["c"] = 3 })
		bom := src[:len(src)-len(strings.TrimPrefix(src, "\xef\xbb\xbf"))]
		want := bom + "{\n  \"a\": 1,\n  \"b\": {\n    \"c\": 3\n  }\n}\n"
		if err != nil || got != want {
			t.Errorf("Edit(%q) = %q, %v; want %q", src, got, err, want)
		}
	}
}

func TestEditRefusesARepeatedKeyOnlyWhereItEdits(t *testing.T) {
	const src = `/**/{"keep": {"x": 1, "x": 2}, "edit": {"y": 1, "y": 2}}`
	if _, err := edit(t, src, func(o map[string]any) { o["new"] = true }); err != nil {
		t.Errorf("edit beside repeated keys: %v", err)
	}
	_, err := edit(t, src, func(o map[string]any) { o["edit"].(map[string]any)["z"] = 3 })
	if err == nil || !strings.Contains(err.Error(), `f.json repeats the key "y"; fix or move it, then retry`) {
		t.Errorf("edit into repeated keys: %v", err)
	}
}

func TestEditRefusesWhatIsNotAnObject(t *testing.T) {
	for _, src := range []string{"[]", "{} {}", `{"a": bogus}`, "{\n  \"a\": bogus\n}", `{"a": }`, `{"a": 1`, "{} /* open"} {
		if _, err := harness.Edit("f.json", []byte(src), map[string]any{"a": 1}); err == nil || !strings.Contains(err.Error(), "is not a JSON object") {
			t.Errorf("Edit(%q) = %v, want not-an-object refusal", src, err)
		}
	}
}

func TestEditKeepsMultiLineBlockCommentsIntact(t *testing.T) {
	const src = "{ /* header 1\n  header 2 */\n  \"a\": 1, /* a1\n  a2 */\n  \"b\": 2 /* b1\n  b2 */\n}"
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
		want   string
	}{
		{"drop first", func(o map[string]any) { delete(o, "a") }, "{ /* header 1\n  header 2 */\n  \"b\": 2 /* b1\n  b2 */\n}"},
		{"drop second", func(o map[string]any) { delete(o, "b") }, "{ /* header 1\n  header 2 */\n  \"a\": 1 /* a1\n  a2 */\n}"},
		{"add third", func(o map[string]any) { o["c"] = 3 }, "{ /* header 1\n  header 2 */\n  \"a\": 1, /* a1\n  a2 */\n  \"b\": 2, /* b1\n  b2 */\n  \"c\": 3\n}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := edit(t, src, tc.change)
			if err != nil || got != tc.want {
				t.Errorf("Edit = %v\n%s\nwant\n%s", err, got, tc.want)
			}
		})
	}
}

func TestEditResultDecodesAsTheObject(t *testing.T) {
	const src = "{\n  // c\n  \"a\": [1, {\"b\": \"x\"}], /* d */ \"e\": {\"f\": {\"g\": null}},\n  \"h\": \"\\u0041\"\n}"
	changes := []func(map[string]any){
		func(o map[string]any) { o["e"].(map[string]any)["f"] = map[string]any{"g": 1, "i": []any{}} },
		func(o map[string]any) { delete(o, "a"); delete(o, "e") },
		func(o map[string]any) { o["h"] = "A&<>" },
		func(o map[string]any) { o["e"] = "flat" },
		func(o map[string]any) { delete(o["e"].(map[string]any), "f"); o["z"] = map[string]any{} },
	}
	for i, change := range changes {
		got, err := edit(t, src, change)
		if err != nil {
			t.Fatalf("change %d: %v", i, err)
		}
		_, decoded, err := harness.ReadJSON(writeTemp(t, got))
		if err != nil {
			t.Fatalf("change %d: %q does not decode: %v", i, got, err)
		}
		_, want, _ := harness.ReadJSON(writeTemp(t, src))
		change(want)
		a, _ := json.Marshal(decoded)
		b, _ := json.Marshal(want)
		if string(a) != string(b) {
			t.Errorf("change %d: %s, want %s", i, a, b)
		}
		// The comment belongs to "a", above it.
		if _, ok := want["a"]; ok != strings.Contains(got, "// c") {
			t.Errorf("change %d: comment kept or lost with its member: %q", i, got)
		}
	}
}

func FuzzEditKeepsTheFileValid(f *testing.F) {
	for _, src := range []string{
		`{}`, `{, }`, `{"a": 1}`, "{\n  \"a\": {\"b\": [1, 2]},\n  // c\n  \"d\": \"e\",\n}", `{"a": {}, "b": {"c": {"d": 1}}}`,
		"{ /* x */ \"a\" : 1 , \"b\":2/* y */}", "{\n\t\"a\": 1, \"b\": 2,\n\t\"c\": 3 // z\n}",
		"{\r\n  \"a\": 1, /* x\r\n */ \"b\": 2\r\n}",
	} {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		_, obj, err := harness.ReadJSON(writeTemp(t, src))
		if err != nil || strings.TrimSpace(src) == "" {
			return
		}
		keys := slices.Sorted(maps.Keys(obj))
		for i, key := range keys {
			switch i % 3 {
			case 0:
				delete(obj, key)
			case 1:
				if inner, ok := obj[key].(map[string]any); ok {
					inner["added"] = []any{"x"}
				} else {
					obj[key] = "replaced"
				}
			}
		}
		obj["zz"] = map[string]any{"n": 1}
		if _, err := harness.Edit("f.json", []byte(src), obj); err != nil && !strings.Contains(err.Error(), "repeats the key") {
			t.Errorf("Edit(%q): %v", src, err)
		}
	})
}

func TestStripRemovesOnlyTheSyntaxNamed(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		syntax    harness.Syntax
		want      string
	}{
		{"a comment line", "{\n  // mine\n  \"a\": 1\n}\n", harness.Strict, "{\n  \"a\": 1\n}\n"},
		{"a comment after a value", "{\n  \"a\": 1, // mine\n  \"b\": 2\n}\n", harness.Strict, "{\n  \"a\": 1,\n  \"b\": 2\n}\n"},
		{"a block over lines", "{\n  /* one\n     two */\n  \"a\": 1\n}\n", harness.Strict, "{\n  \"a\": 1\n}\n"},
		{"a block inside a line", "{\"a\": /* x */ 1}", harness.Strict, "{\"a\": 1}"},
		{"a block opening a line", "{\n  /* x */ \"a\": 1\n}", harness.Strict, "{\n  \"a\": 1\n}"},
		{"a trailing comma", "{\n  \"a\": [1, 2,],\n  \"b\": 2,\n}\n", harness.Strict, "{\n  \"a\": [1, 2],\n  \"b\": 2\n}\n"},
		{"a comma before a comment and the bracket", "{\"a\": 1, // x\n}", harness.Strict, "{\"a\": 1\n}"},
		{"a lone comma", "{,}", harness.Strict, "{}"},
		{"CRLF", "{\r\n  // mine\r\n  \"a\": 1 // x\r\n}\r\n", harness.Strict, "{\r\n  \"a\": 1\r\n}\r\n"},
		{"a BOM", "\xef\xbb\xbf// mine\n{}\n", harness.Strict, "\xef\xbb\xbf{}\n"},
		{"strings", "{\"a\": \"// no /* no */ ,]\", \"b\\\"//\": 1}", harness.Strict, "{\"a\": \"// no /* no */ ,]\", \"b\\\"//\": 1}"},
		{"blocks only", "{\n  // keep\n  /* drop */\n  \"a\": 1,\n}\n", harness.BlockComments, "{\n  // keep\n  \"a\": 1,\n}\n"},
		{"a line comment holding a block opener", "{\n  // keep /*\n  \"a\": 1\n}\n", harness.BlockComments, "{\n  // keep /*\n  \"a\": 1\n}\n"},
		{"an unterminated block", "{\"a\": 1 /* x", harness.Strict, "{\"a\": 1 /* x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(harness.Strip([]byte(tc.src), tc.syntax)); got != tc.want {
				t.Errorf("Strip(%q) = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

// What JSON.parse reads is what the JSONC said: Strict leaves plain JSON with the same value.
func FuzzStripLeavesTheSameValue(f *testing.F) {
	for _, src := range []string{
		"{\n  // c\n  \"a\": [1, 2,], /* d */ \"e\": {\"f\": null,},\n}", "{, }", "{\"a\": \"//\\\"/*\"} // x",
		"\xef\xbb\xbf{\r\n  /* x\r\n */ \"b\": 2, // y\r\n}\r\n",
	} {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		_, want, err := harness.ReadJSON(writeTemp(t, src))
		if err != nil || strings.TrimSpace(src) == "" {
			return
		}
		out := harness.Strip([]byte(src), harness.Strict)
		var got map[string]any
		dec := json.NewDecoder(strings.NewReader(strings.TrimPrefix(string(out), "\xef\xbb\xbf")))
		dec.UseNumber()
		if !json.Valid([]byte(strings.TrimPrefix(string(out), "\xef\xbb\xbf"))) || dec.Decode(&got) != nil {
			t.Fatalf("Strip(%q) = %q, not JSON", src, out)
		}
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(want)
		if string(a) != string(b) {
			t.Errorf("Strip(%q) = %s, want %s", src, a, b)
		}
	})
}
