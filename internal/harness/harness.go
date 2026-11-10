// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package harness holds what a launch command hands to the exec step.
package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

// Input is what the table-driven harnesses (codex, pi, opencode) need; the table is non-empty.
type Input struct {
	Table   []models.Model
	BaseURL string
	// UserHome is $HOME, which holds the harness's own configuration.
	UserHome string
	// ConfigHome is XDG_CONFIG_HOME; empty means UserHome/.config.
	ConfigHome string
	// Args are forwarded to the harness untouched; without any, ModelArgs adds a model choice.
	Args []string
	// Default makes the gateway the native default only during explicitly selected setup.
	Default bool
}

// File is generated harness configuration. It never carries the gateway key.
type File struct {
	Path string
	Data []byte
	// Backup copies a file the user already has to <Path>.aiven-<UTC time>.bak before it is replaced.
	Backup bool
	// Models marks a file that lists every gateway model; a preview names a few models instead of diffing it.
	Models bool
	// Reference reports whether value, in a field named like a credential (field, lower case), names where a key is
	// in this file's own syntax rather than holding one, so a preview may show it. Nil means no value does.
	Reference func(field, value string) bool
	// Before and Existed identify the inspected version; setup refuses a changed file before publication.
	Before    []byte
	Existed   bool
	Inspected bool
	// Perm holds the inspected file's permission bits, which its replacement keeps.
	Perm os.FileMode
	// Parents are checked again at apply, so a directory swapped for a symlink cannot redirect the write.
	Parents []string
}

// Plan describes either setup files or a launch argv/environment. Launch refuses plans with files.
type Plan struct {
	Files []File
	Argv  []string
	Env   []string
}

// ModelArgs chooses model with flag when there are no native args. Native args pass unchanged: where a model flag
// may go among them depends on the harness's subcommands and the options that take values, which only it parses.
func ModelArgs(args []string, model, flag string) []string {
	if len(args) > 0 {
		return args
	}
	return []string{flag, model}
}

// ReadObject parses the JSON/JSONC object at path, keeping numbers verbatim; a missing file is an empty object.
func ReadObject(path string) (map[string]any, error) {
	_, obj, err := ReadJSON(path)
	return obj, err
}

// ReadJSON is ReadObject that also returns the content of the file, for Edit; a missing file has none.
func ReadJSON(path string) ([]byte, map[string]any, error) {
	data, err := fsutil.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, map[string]any{}, nil
	} else if err != nil {
		return nil, nil, err
	}
	v, err := decodeJSON(data)
	if obj, ok := v.(map[string]any); err == nil && ok {
		return data, obj, nil
	}
	return nil, nil, fmt.Errorf("%s is not a JSON object; fix or move it, then retry", path)
}

func stripJSONC(src []byte) []byte {
	src = bytes.TrimPrefix(src, []byte("\xef\xbb\xbf"))
	out := make([]byte, 0, len(src))
	for i, inStr := 0, false; i < len(src); i++ {
		switch c := src[i]; {
		case inStr && c == '\\' && i+1 < len(src):
			out = append(out, c, src[i+1])
			i++
		case c == '"':
			inStr, out = !inStr, append(out, c)
		case !inStr && c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i += 2; i < len(src) && src[i] != '\n'; i++ {
			}
			i--
		case !inStr && c == '/' && i+1 < len(src) && src[i+1] == '*':
			// A comment separates tokens like whitespace; an unterminated one stays in so decoding fails rather than
			// silently dropping the rest of the file.
			end := bytes.Index(src[i+2:], []byte("*/"))
			if end < 0 {
				return append(out, src[i:]...)
			}
			out = append(out, ' ')
			i += end + 3
		default:
			for j := len(out) - 1; !inStr && (c == '}' || c == ']') && j >= 0 && out[j] <= ',' && (out[j] == ',' || out[j] <= ' '); j-- {
				if out[j] == ',' {
					out[j] = ' '
					break
				}
			}
			out = append(out, c)
		}
	}
	return out
}

// Object returns parent[key] of the file at path as an object, adding an empty one when absent.
func Object(parent map[string]any, key, path string) (map[string]any, error) {
	if parent[key] == nil {
		parent[key] = map[string]any{}
	}
	if v, ok := parent[key].(map[string]any); ok {
		return v, nil
	}
	return nil, fmt.Errorf("%s: %q is not a JSON object; fix or move it, then retry", path, key)
}

// JSON renders v as 2-space indented JSON with a trailing newline. '&', '<' and '>' stay unescaped, so a key
// helper's && reads as written.
func JSON(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v) // plain structs and maps cannot fail to marshal
	return b.Bytes()
}
