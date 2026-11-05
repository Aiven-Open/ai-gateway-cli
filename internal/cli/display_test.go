// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/omp"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/opencode"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/pi"
)

func TestRedactKeepsOnlyValuesThatNameWhereAKeyIs(t *testing.T) {
	// A credential field's value can be any string, so a value stays only when aiven-ai-gateway wrote it or the
	// file's own syntax makes it a reference; the same reference text in another tool's file is a literal.
	for _, tc := range []struct {
		file         string
		reference    func(field, value string) bool
		kept, hidden []string
	}{
		{
			file: "any", kept: []string{
				`"apiKey": ` + strconv.Quote(pi.APIKey(config.DefaultBaseURL)),
				`"apiKey": ` + strconv.Quote(pi.APIKey("http://127.0.0.1:4000")),
				`"apiKey": ` + strconv.Quote(omp.APIKey("https://gw.example:8443")),
				`"apiKey": ` + strconv.Quote(opencode.APIKey(config.DefaultBaseURL)),
				`"apiKey": ` + strconv.Quote(opencode.APIKey("http://[::1]:8080")),
				`"apiKeyHelper": ` + strconv.Quote(claude.APIKeyHelper(config.DefaultBaseURL)),
				`"apiKeyHelper": ` + strconv.Quote(claude.APIKeyHelper("http://127.0.0.1:4000")),
				`"apiKey": ` + strconv.Quote(creds.EnvVar),
				`"ANTHROPIC_AUTH_TOKEN": ""`,
				`"baseUrl": "https://ai.aiven.io/v1"`,
			}, hidden: []string{
				`"apiKeyHelper": "/usr/local/bin/get-key"`,
				`"password": "fixture pass phrase"`,
				`"apiKey": "!echo s3cret"`,
				`"apiKey": ` + strconv.Quote(strings.Replace(pi.APIKey(config.DefaultBaseURL), "cat ", "echo s3cret; cat ", 1)),
				`"apiKey": ` + strconv.Quote(strings.Replace(opencode.APIKey("http://127.0.0.1:4000"), "ai-gateway-keys/", "ai-gateway-keys/s3cret/", 1)),
				`"token": "~/s3cret"`,
				`"secret": "$S3CRET"`,
				`"secret": "UPPER_SECRET_123"`,
				`"DATABASE_PASSWORD": "{env:SYNTHETIC_PASSWORD}"`,
				`"DATABASE_PASSWORD": "{file:/synthetic-secret}"`,
				`env_key = "OPENAI_API_KEY"`,
				`model_providers.other.env_key = "OTHER_KEY"`,
				`experimental_bearer_token = "OPENAI_API_KEY"`,
			},
		},
		{
			file: "opencode.json", reference: opencode.Reference,
			kept:   []string{`"apiKey": "{env:OPENAI_API_KEY}"`, `"apiKey": "{file:~/.secrets/openai}"`},
			hidden: []string{`"apiKey": "{env:x} and more"`, `"password": "fixture pass phrase"`, `"apiKey": "OPENAI_API_KEY"`},
		},
	} {
		for _, line := range tc.kept {
			if got := newRedactor(tc.reference).redact(line); got != line {
				t.Errorf("%s: redact(%s) = %s, want it unchanged", tc.file, line, got)
			}
		}
		for _, line := range tc.hidden {
			if got := newRedactor(tc.reference).redact(line); !strings.Contains(got, "<redacted 1>") {
				t.Errorf("%s: redact(%s) = %s, want the value hidden", tc.file, line, got)
			}
		}
	}
	// Two hosts that differ only inside a hidden value must still diff as different.
	r := newRedactor(nil)
	stage, prod := r.redact(`"baseURL": "https://sk-gateway-stage.example.invalid/v1"`), r.redact(`"baseURL": "https://sk-gateway-production.example.invalid/v1"`)
	if stage == prod || strings.Contains(stage+prod, "sk-gateway") || r.redact(`"token": "sk-gateway-stage.example.invalid"`) != `"token": "<redacted 1>"` {
		t.Errorf("stage %s, prod %s", stage, prod)
	}
}

func TestRedactHidesTheValuesInsideACredentialListOrObject(t *testing.T) {
	for in, want := range map[string]string{
		`"credentials": {"primary": "opaque", "spare": ["two", ""]}, "theme": "old"`:                     `"credentials": {"primary": "<redacted 1>", "spare": ["<redacted 2>", ""]}, "theme": "old"`,
		"api_keys = ['opaque', \"two\"]\ntheme = \"old\"\n":                                              "api_keys = ['<redacted 1>', \"<redacted 2>\"]\ntheme = \"old\"\n",
		`"auth": {"token": "opaque", "helper": ` + strconv.Quote(pi.APIKey(config.DefaultBaseURL)) + `}`: `"auth": {"token": "<redacted 1>", "helper": ` + strconv.Quote(pi.APIKey(config.DefaultBaseURL)) + `}`,
		"secrets = [\"opaque\n":                                                           "secrets = [\"<redacted 1>\n",
		"secrets = [\"\n\"opaque\"]\n":                                                    "secrets = [\"<redacted 1>\n\"<redacted 2>\"]\n",
		`auth = {"primary" = "opaque"}`:                                                   `auth = {"primary" = "<redacted 1>"}`,
		"api_keys = [\"\"\"\nopaque\\\"\"\"\n\"\"\", '''two'value''']\n":                  "api_keys = [\"\"\"<redacted 1>\"\"\", '''<redacted 2>''']\n",
		"secrets = ['''opaque\n":                                                          "secrets = ['''<redacted 1>",
		"api_keys = [\n  # disabled: ]\n  \"opaque\",\n]\n":                               "api_keys = [\n  # <redacted 1>\n  \"<redacted 2>\",\n]\n",
		"\"auth\": {\n  // disabled: }\n  /* block: } */\n  \"primary\": \"opaque\"\n}\n": "\"auth\": {\n  // <redacted 1>\n  /* <redacted 2>*/\n  \"primary\": \"<redacted 3>\"\n}\n",
		`{"note": "rotate key: [weekly", "api_keys": ["opaque"]}`:                         `{"note": "rotate key: [weekly", "api_keys": ["<redacted 1>"]}`,
		"# rotate key: [weekly\ntoken = \"opaque\"\n":                                     "# <redacted 2>\ntoken = \"<redacted 1>\"\n",
		"# rotate key: [weekly\ntheme = 42\n":                                             "# <redacted 1>\ntheme = 42\n",
		"secrets = [\"\"\"opaque\"\"\" = 1]\n":                                            "secrets = [\"\"\"<redacted 1>\"\"\" = <redacted 2>]\n",
		`{"credentials": {"pin": 1234, "spare": [-5.5e3]}, "token": 42, "theme": 42}`:     `{"credentials": {"pin": <redacted 1>, "spare": [<redacted 2>]}, "token": <redacted 3>, "theme": 42}`,
		"auth = { 1234 = \"opaque\", on = true, at = 1979-05-27T07:32:00Z }\n":            "auth = { 1234 = \"<redacted 1>\", on = true, at = <redacted 2> }\n",
	} {
		if got := newRedactor(nil, in).redact(in); got != want {
			t.Errorf("redact(%q) = %q, want %q", in, got, want)
		}
	}
}

// Each case is one gap a review found in the preview masking.
func TestRedactHidesEveryReportedCredentialForm(t *testing.T) {
	for in, want := range map[string]string{
		// Gateway keys use the whole key charset, punctuation included.
		`"note": "AVNS__opaque"`:     `"note": "<redacted 1>"`,
		`"note": "AVNS_-opaque"`:     `"note": "<redacted 1>"`,
		`"note": "AVNS_op.aq-ue_x"`:  `"note": "<redacted 1>"`,
		`"note": "sk-opaque.value1"`: `"note": "<redacted 1>"`,
		// A basic multi-line string ends at its first unescaped closing quotes, not at an escaped or literal one.
		"token = \"\"\"opaque\\\"\"\"still secret\"\"\"\n": "token = \"\"\"<redacted 1>\"\"\"\n",
		"token = \"\"\"opaque''' still secret\"\"\"\n":     "token = \"\"\"<redacted 1>\"\"\"\n",
		"token = '''opaque\"\"\" still secret'''\n":        "token = '''<redacted 1>'''\n",
		// A block comment that mentions a credential is hidden to its end.
		"{\n  /* the token:\n     opaque */\n  \"theme\": \"dark\"\n}\n": "{\n  /* <redacted 1>*/\n  \"theme\": \"dark\"\n}\n",
		"{\n  /* layout\n     notes */ \"theme\": \"dark\"\n}\n":         "{\n  /* layout\n     notes */ \"theme\": \"dark\"\n}\n",
		// Header and PAT fields, and raw GitHub and AWS tokens in any field.
		`"ANTHROPIC_CUSTOM_HEADERS": "X-Api: opaque"`:          `"ANTHROPIC_CUSTOM_HEADERS": "<redacted 1>"`,
		`"OTEL_EXPORTER_OTLP_HEADERS": "authorization=opaque"`: `"OTEL_EXPORTER_OTLP_HEADERS": "<redacted 1>"`,
		`"GITHUB_PAT": "opaque"`:                               `"GITHUB_PAT": "<redacted 1>"`,
		`"note": "ghp_0123456789abcdefghijABCD"`:               `"note": "<redacted 1>"`,
		`"note": "github_pat_0123456789abcdefghij_ABCD"`:       `"note": "<redacted 1>"`,
		`"note": "AKIA0123456789ABCDEF"`:                       `"note": "<redacted 1>"`,
		// pat counts only as a whole part of a name.
		`"keyPath": "~/k", "path": "/p", "pattern": "x"`: `"keyPath": "<redacted 1>", "path": "/p", "pattern": "x"`,
	} {
		if got := newRedactor(nil, in).redact(in); got != want {
			t.Errorf("redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPreviewHidesCredentialListsWithCommentsAndMultilineStrings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, file, data string
	}{
		{"TOML comment", "config.toml", "tokens = [\"first-private\", # ]\n \"second-private\"]\ntheme = \"old\"\n"},
		{"TOML multiline basic string", "config.toml", "tokens = [\"\"\"first-private\nsecond-private\"\"\"]\ntheme = \"old\"\n"},
		{"TOML multiline literal string", "config.toml", "tokens = ['''first-private\nsecond-private''']\ntheme = \"old\"\n"},
		{"JSONC line comment", "models.json", "{\"tokens\": [\"first-private\", // ]\n \"second-private\"], \"theme\": \"old\"}\n"},
		{"JSONC block comment", "models.json", "{\"tokens\": [\"first-private\", /* ] */ \"second-private\"], \"theme\": \"old\"}\n"},
		{"JSONC multiline block comment", "models.json", "{\"tokens\": [\"first-private\", /* ]\n */ \"second-private\"], \"theme\": \"old\"}\n"},
		{"JSONC credential comment", "models.json", "{\"tokens\": [\"first-private\", /* token comment */\n \"second-private\"], \"theme\": \"old\"}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var decoded map[string]any
			var err error
			if strings.HasSuffix(tc.file, ".toml") {
				err = toml.Unmarshal([]byte(tc.data), &decoded)
			} else {
				path := filepath.Join(t.TempDir(), tc.file)
				writeTestFile(t, path, tc.data)
				decoded, err = harness.ReadObject(path)
			}
			if err != nil || decoded["theme"] != "old" {
				t.Fatalf("invalid fixture: %v, %v", decoded, err)
			}
			f := harness.File{Path: tc.file, Data: []byte(strings.Replace(tc.data, "old", "new", 1))}
			before, after := shown(f, []byte(tc.data), nil)
			for side, text := range map[string]string{"before": before, "after": after} {
				for _, secret := range []string{"first-private", "second-private"} {
					if strings.Contains(text, secret) {
						t.Errorf("%s preview discloses %q: %q", side, secret, text)
					}
				}
				theme := "old"
				if side == "after" {
					theme = "new"
				}
				if !strings.Contains(text, "theme") || !strings.Contains(text, strconv.Quote(theme)) ||
					!strings.Contains(text, "<redacted ") {
					t.Errorf("%s preview lost unrelated context or redaction: %q", side, text)
				}
			}
		})
	}
}

func TestRedactHidesAValueThatReadsLikeAPlaceholder(t *testing.T) {
	t.Parallel()
	for _, secret := range []string{"<redacted 1>", "<redacted 917>"} {
		for _, in := range []string{
			`"password": "` + secret + `"`,
			`"credentials": {"password": "` + secret + `"}`,
			`"credentials": ["` + secret + `"]`,
		} {
			if got := newRedactor(nil, in).redact(in); strings.Contains(got, secret) {
				t.Errorf("redact(%q) = %q, want the value hidden", in, got)
			}
		}
	}
	for _, ext := range []string{".json", ".yaml"} {
		old, next := `"password": "first-synthetic"`, `"password": "<redacted 1>"`
		if ext == ".yaml" {
			old, next = "password: first-synthetic\n", "password: '<redacted 1>'\n"
		}
		before, after := shown(harness.File{Path: "config" + ext, Data: []byte(next)}, []byte(old), nil)
		if strings.Contains(before+after, "first-synthetic") || strings.Contains(before+after, "<redacted 1>") ||
			strings.ContainsAny(before+after, "\x00") || before == after {
			t.Errorf("%s: before %q, after %q", ext, before, after)
		}
	}
	// A decoded YAML value can hold the marker itself, through an alias too.
	for _, old := range []string{`password: "\0<redacted 1>"`, "ordinary: &shared \"\\0<redacted 1>\"\npassword: *shared\n"} {
		before, after := shown(harness.File{Path: "config.yaml", Data: []byte("password: next\n")}, []byte(old), nil)
		if strings.Contains(before+after, "<redacted 1>") {
			t.Errorf("%q: before %q, after %q", old, before, after)
		}
	}
}

func TestCollapseModelsTouchesOnlyTheGatewayProvidersList(t *testing.T) {
	text := string(harness.JSON(map[string]any{
		"mcp":      map[string]any{"aiven": map[string]any{"models": []any{"keep-me"}}},
		"provider": map[string]any{"aiven": map[string]any{"models": map[string]any{"m1": map[string]any{}}, "npm": "x"}},
	}))
	block, got := collapseModels(text, "[S]")
	if !strings.Contains(got, `"keep-me"`) || !strings.Contains(got, `      "models": [S],`) || !strings.Contains(block, `"m1"`) {
		t.Errorf("block %q, collapsed %s", block, got)
	}
}

func TestCollapseYAMLModelsTouchesOnlyTheGatewayProvidersList(t *testing.T) {
	const text = "mcp:\n  aiven:\n    models:\n    - keep-me\nproviders:\n  aiven:\n    models:\n    - id: m1\n      # first\n" +
		"      name: M1\n\n    - id: m2\n    # about npm\n    npm: x\n  local:\n    models:\n    - keep-too\n"
	block, got := collapseYAMLModels(text, "[S]")
	want := "mcp:\n  aiven:\n    models:\n    - keep-me\nproviders:\n  aiven:\n    models: [S]\n    # about npm\n    npm: x\n" +
		"  local:\n    models:\n    - keep-too\n"
	if got != want || !strings.HasSuffix(block, "    - id: m2\n") {
		t.Errorf("block %q, collapsed %q", block, got)
	}
}

func TestShownSummarisesAnUnchangedYAMLModelListOnBothSides(t *testing.T) {
	const list = "    models:\n    - id: glm-5\n      name: GLM 5\n"
	before := "providers:\n  aiven:\n    baseUrl: https://old.invalid/v1\n" + list
	after := "providers:\n  aiven:\n    baseUrl: https://new.invalid/v1\n" + list
	summary := "    models: [3 models: claude-sonnet-5-5, glm-5, … +1 more]\n"
	from, to := shown(harness.File{Path: "models.yml", Data: []byte(after), Models: true}, []byte(before), previewTable)
	if !strings.HasSuffix(from, summary) || !strings.HasSuffix(to, summary) {
		t.Errorf("same list: %q / %q", from, to)
	}
	from, _ = shown(harness.File{Path: "models.yml", Data: []byte(after + "    - id: m2\n"), Models: true}, []byte(before), previewTable)
	if !strings.HasSuffix(from, "    models: [the previous model list]\n") {
		t.Errorf("changed list: %q", from)
	}
}
