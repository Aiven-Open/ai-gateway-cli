// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package codex

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// editTOML decodes src, applies change to the result and edits src to match it.
func editTOML(t *testing.T, src string, change func(cfg map[string]any)) (string, error) {
	t.Helper()
	cfg := map[string]any{}
	if err := toml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatal(err)
	}
	change(cfg)
	out, err := edit("config.toml", []byte(src), cfg)
	return string(out), err
}

func table(cfg map[string]any, path ...string) map[string]any {
	for _, k := range path {
		next, ok := cfg[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cfg[k] = next
		}
		cfg = next
	}
	return cfg
}

func TestEditRewritesOnlyWhatChanges(t *testing.T) {
	const src = `# Codex
model = "o3" # mine
profile = "aiven"

# legacy
[profiles.aiven]
model = "x"

[profiles.work]
model = "o3"

[model_providers.aiven]
# old key
env_key = "AIVEN_GATEWAY_KEY"
name = "old"
query_params = { a = "1" }
`
	got, err := editTOML(t, src, func(cfg map[string]any) {
		delete(cfg, "profile")
		delete(table(cfg, "profiles"), "aiven")
		provider := table(cfg, "model_providers", "aiven")
		delete(provider, "env_key")
		provider["name"], provider["base_url"] = "Aiven AI Gateway", "https://gateway.example/v1"
		provider["auth"] = map[string]any{"command": "sh", "args": []string{"-c", "helper"}}
	})
	const want = `# Codex
model = "o3" # mine

[profiles.work]
model = "o3"

[model_providers.aiven]
name = 'Aiven AI Gateway'
query_params = { a = "1" }
base_url = 'https://gateway.example/v1'

[model_providers.aiven.auth]
args = ['-c', 'helper']
command = 'sh'
`
	if err != nil || got != want {
		t.Errorf("edit = %v\n%s\nwant\n%s", err, got, want)
	}
	if got, err := editTOML(t, src, func(map[string]any) {}); err != nil || got != src {
		t.Errorf("no change: %v\n%s", err, got)
	}
}

func TestEditAddsKeysWhereTheirTableIs(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		change    func(map[string]any)
		want      string
	}{
		{"root before the first table", "# top\n[x]\na = 1\n", func(c map[string]any) { c["model"] = "m" },
			"model = 'm'\n\n# top\n[x]\na = 1\n"},
		{"root after its keys", "a = 1\n\n[x]\nb = 2\n", func(c map[string]any) { c["model"] = "m" },
			"a = 1\nmodel = 'm'\n\n[x]\nb = 2\n"},
		{"no final newline", "a = 1", func(c map[string]any) { c["b"] = 2 }, "a = 1\nb = 2\n"},
		{"a key and a table after no final newline", "[x]\na = 1", func(c map[string]any) { table(c, "x")["b"] = 2; table(c, "y")["c"] = 3 },
			"[x]\na = 1\nb = 2\n\n[y]\nc = 3\n"},
		{"a table after a removed last line", "[x]\na = 1\nb = 2", func(c map[string]any) { delete(table(c, "x"), "b"); table(c, "y")["c"] = 3 },
			"[x]\na = 1\n\n[y]\nc = 3\n"},
		{"a table only its subtables declared", "[a.b]\nc = 1\n", func(c map[string]any) { table(c, "a")["d"] = 2 },
			"[a.b]\nc = 1\n\n[a]\nd = 2\n"},
		{"a dotted table", "[p]\nauth.command = 'sh'\n\n[q]\n", func(c map[string]any) { table(c, "p", "auth")["args"] = []string{"x"} },
			"[p]\nauth.command = 'sh'\nauth.args = ['x']\n\n[q]\n"},
		{"beside an array of tables", "[[x]]\na = 1\n[[x]]\na = 2\n", func(c map[string]any) { c["b"] = true },
			"b = true\n\n[[x]]\na = 1\n[[x]]\na = 2\n"},
		{"a removed array of tables", "[[p]]\na = 1\n[p.sub]\nb = 2\n# about q\n[q]\n", func(c map[string]any) { delete(c, "p") },
			"# about q\n[q]\n"},
		{"a quoted key", "[p]\n", func(c map[string]any) { table(c, "p")["a b"] = 1 }, "[p]\n'a b' = 1\n"},
		{"an emptied table", "[p]\na = 1\n# about q\n[q]\n", func(c map[string]any) { delete(c, "p") }, "# about q\n[q]\n"},
		{"CRLF line ends", "a = 1\r\n\r\n[x]\r\nb = 2\r\n", func(c map[string]any) { c["m"] = "m"; table(c, "y")["c"] = 3 },
			"a = 1\r\nm = 'm'\r\n\r\n[x]\r\nb = 2\r\n\r\n[y]\r\nc = 3\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := editTOML(t, tc.src, tc.change)
			if err != nil || got != tc.want {
				t.Errorf("edit = %v\n%q\nwant\n%q", err, got, tc.want)
			}
		})
	}
}

func TestEditRefusesAnAivenProviderOutsideItsTable(t *testing.T) {
	for _, src := range []string{
		"model_providers.aiven.name = 'x'\n",
		"model_providers = { aiven = { name = 'x' } }\n",
		"model_providers = {}\n",
		"[model_providers]\naiven = { name = 'x' }\n",
		"[model_providers]\naiven.name = 'x'\n",
		"[[model_providers.aiven]]\nname = 'x'\n",
	} {
		_, err := editTOML(t, src, func(c map[string]any) { c["model"] = "m" })
		if err == nil || !strings.Contains(err.Error(), "config.toml declares model_providers in a form setup does not edit") {
			t.Errorf("edit(%q) = %v", src, err)
		}
	}
	for _, src := range []string{"[model_providers]\nother = { name = 'x' }\n", "[model_providers.aiven.auth]\ncommand = 'sh'\n"} {
		if _, err := editTOML(t, src, func(c map[string]any) { c["model"] = "m" }); err != nil {
			t.Errorf("edit(%q) = %v", src, err)
		}
	}
}

func FuzzEditKeepsTheFileValid(f *testing.F) {
	for _, src := range []string{
		"", "a = 1\n", "# c\n[x]\na = 1 # d\n\n[x.y]\nb = [1,\n  2]\n", "[[x]]\na = 1\n[x.sub]\nb = 2\n",
		"[model_providers.aiven]\nenv_key = 'K'\n[model_providers.aiven.auth]\ncommand = 'sh'\n", "p.q = 1\n[r]\ns.t = '''\nu\n'''\n",
		"profile = 'aiven'\n[profiles.aiven]\nmodel = 'x'\n[profiles]\n", "[profiles.aiven]", "['']",
		"[[profiles.aiven]]\nmodel = 'x'\n[profiles.aiven.sub]\ny = 1\n", "[[model]]\nx = 1\n", "a = nan\r\n[x]\r\nb = '''\r\nu\r\n'''\r\n",
	} {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		cfg := map[string]any{}
		if toml.Unmarshal([]byte(src), &cfg) != nil {
			return
		}
		out, err := edit("config.toml", []byte(src), cfg)
		if err != nil && !strings.Contains(err.Error(), "declare the Aiven provider as a [model_providers.aiven] table") ||
			err == nil && strings.TrimSpace(src) != "" && string(out) != src {
			t.Errorf("unchanged edit(%q) = %q, %v", src, out, err)
		}
		delete(cfg, "profile")
		if profiles, ok := cfg["profiles"].(map[string]any); ok {
			delete(profiles, "aiven")
		}
		if providers, ok := cfg["model_providers"].(map[string]any); ok || cfg["model_providers"] == nil {
			if providers == nil {
				providers = map[string]any{}
				cfg["model_providers"] = providers
			}
			provider, ok := providers["aiven"].(map[string]any)
			if !ok {
				if providers["aiven"] != nil {
					return
				}
				provider = map[string]any{}
				providers["aiven"] = provider
			}
			delete(provider, "env_key")
			provider["name"], provider["auth"] = "Aiven", map[string]any{"command": "sh", "args": []string{"-c", "h"}}
		}
		cfg["model"] = "m"
		if _, err := edit("config.toml", []byte(src), cfg); err != nil && !strings.Contains(err.Error(), "declare the Aiven provider as a [model_providers.aiven] table") {
			t.Errorf("edit(%q): %v", src, err)
		}
	})
}
