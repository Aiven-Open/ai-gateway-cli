// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package codex

import (
	"bytes"
	"fmt"
	"maps"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// tomlEntry is a key-value or a table header of a TOML document, with the lines it spans.
type tomlEntry struct {
	header bool
	// array is the [[array]] table the entry is or sits under, which edit keeps or drops whole.
	array []string
	// path is the header's table or the key-value's full key; table is the table a key-value sits in.
	path, table []string
	// start and end span the entry's lines; value spans a key-value's value.
	start, end, valueStart, valueEnd int
}

type tomlSplice struct {
	start, end int
	text       string
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// edit returns src, the TOML document at path, changed to decode as cfg. Only keys whose values differ are rewritten
// in place or removed, a table cfg no longer holds goes with its lines, a new key joins its table and a new table goes
// at the end, so comments, order and layout elsewhere stay. A file that defines model_providers.aiven other than as a
// [model_providers.aiven] table is refused, since its keys cannot be replaced in place.
func edit(path string, src []byte, cfg map[string]any) ([]byte, error) {
	data, err := toml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(src)) == 0 {
		return data, nil
	}
	want, have := map[string]any{}, map[string]any{}
	if err := toml.Unmarshal(data, &want); err != nil {
		return nil, err
	}
	entries, err := parseTOML(src)
	if err != nil || toml.Unmarshal(src, &have) != nil {
		return nil, fmt.Errorf("%s is not valid TOML; fix or move it, then retry", path)
	}
	for _, e := range entries {
		aiven := len(e.path) == 1 && e.path[0] == "model_providers" ||
			len(e.path) > 1 && e.path[0] == "model_providers" && e.path[1] == "aiven"
		inTable := e.array == nil && len(e.table) > 1 && e.table[0] == "model_providers" && e.table[1] == "aiven"
		if aiven && (e.array != nil || !e.header && !inTable) {
			return nil, fmt.Errorf("%s declares model_providers in a form setup does not edit; "+
				"declare the Aiven provider as a [model_providers.aiven] table, then retry", path)
		}
	}
	out, err := splice(src, entries, want, have)
	if err != nil {
		return nil, fmt.Errorf("could not edit %s in place; move it, then retry", path)
	}
	// The edit must say exactly what cfg says; anything else is a defect here, and the file is left alone.
	if got := map[string]any{}; toml.Unmarshal(out, &got) != nil || !same(got, want) {
		return nil, fmt.Errorf("could not edit %s in place; move it, then retry", path)
	}
	return out, nil
}

func parseTOML(src []byte) ([]tomlEntry, error) {
	p := unstable.Parser{}
	p.Reset(src)
	var entries []tomlEntry
	var table, array []string
	var arrays [][]string
	for p.NextExpression() {
		e := p.Expression()
		var keys []string
		var first, last *unstable.Node
		for it := e.Key(); it.Next(); {
			if last = it.Node(); first == nil {
				first = last
			}
			keys = append(keys, string(last.Data))
		}
		end := int(last.Raw.Offset + last.Raw.Length)
		switch e.Kind {
		case unstable.Table, unstable.ArrayTable:
			table, array = keys, nil
			for _, a := range arrays {
				if len(keys) > len(a) && slices.Equal(keys[:len(a)], a) {
					array = a
					break
				}
			}
			if e.Kind == unstable.ArrayTable && array == nil {
				array = keys
				arrays = append(arrays, keys)
			}
			start := lineStart(src, int(first.Raw.Offset))
			entries = append(entries, tomlEntry{header: true, array: array, path: keys, start: start, end: lineEnd(src, end)})
		case unstable.KeyValue:
			value := end + bytes.IndexByte(src[end:], '=') + 1
			for value < len(src) && (src[value] == ' ' || src[value] == '\t') {
				value++
			}
			valueEnd := int(e.Raw.Offset + e.Raw.Length)
			entries = append(entries, tomlEntry{
				array: array, path: slices.Concat(table, keys), table: table,
				start: lineStart(src, int(e.Raw.Offset)), end: lineEnd(src, valueEnd), valueStart: value, valueEnd: valueEnd,
			})
		}
	}
	return entries, p.Error()
}

// splice computes the edit. A key-value or header owns the comment lines right above it, so removing it removes them.
func splice(src []byte, entries []tomlEntry, want, have map[string]any) ([]byte, error) {
	var edits []tomlSplice
	// owner is where each table's own key-values end: the root's, a header's, or those of the header whose dotted
	// keys define it.
	type owner struct {
		end int
		// prefix is the dotted key that declares the table within parent's key-values.
		prefix []string
		parent string
	}
	owners := map[string]*owner{"": {end: -1}}
	defined := map[string]bool{}
	tables := map[string]bool{"": true}
	firstHeader := -1
	current := owners[""]
	// Only what stays declares a table: a removed header or key-value no longer does.
	for i, e := range entries {
		if e.header {
			if firstHeader < 0 {
				firstHeader = i
			}
			current = &owner{end: e.end}
			if v, ok := lookup(want, e.path); e.array == nil && !isTable(v, ok) || e.array != nil && !isArray(lookup(want, e.array)) {
				end := len(src)
				for _, next := range entries[i+1:] {
					if next.header {
						end = comments(src, next.start)
						break
					}
				}
				edits = append(edits, tomlSplice{comments(src, e.start), end, ""})
				continue
			}
			for n := 1; n < len(e.path); n++ {
				tables[join(e.path[:n])] = true
			}
			if e.array != nil {
				// An array of tables is kept whole; cfg holds it as a list, not a table.
				defined[join(e.array)] = true
				continue
			}
			tables[join(e.path)] = true
			owners[join(e.path)] = current
			continue
		}
		if e.array != nil {
			continue
		}
		current.end = e.end
		v, ok := lookup(want, e.path)
		if !ok {
			edits = append(edits, tomlSplice{comments(src, e.start), e.end, ""})
			continue
		}
		defined[join(e.path)] = true
		for n := len(e.table) + 1; n < len(e.path); n++ {
			tables[join(e.path[:n])] = true
			if owners[join(e.path[:n])] == nil {
				owners[join(e.path[:n])] = &owner{prefix: e.path[len(e.table):n], parent: join(e.table)}
			}
		}
		if old, _ := lookup(have, e.path); !same(old, v) {
			text, err := renderValue(v)
			if err != nil {
				return nil, err
			}
			edits = append(edits, tomlSplice{e.valueStart, e.valueEnd, text})
		}
	}
	// A dotted table's key-values end with those of the table its keys sit in.
	for _, o := range owners {
		if o.prefix != nil {
			o.end = owners[o.parent].end
		}
	}
	if root := owners[""]; root.end < 0 {
		root.end = len(src)
		if firstHeader >= 0 {
			root.end = comments(src, entries[firstHeader].start)
		}
	}
	var added strings.Builder
	var walk func(table []string, m map[string]any) error
	walk = func(table []string, m map[string]any) error {
		var scalars []string
		for _, k := range slices.Sorted(maps.Keys(m)) {
			p := append(slices.Clone(table), k)
			if defined[join(p)] {
				continue
			}
			if sub, isMap := m[k].(map[string]any); isMap {
				if tables[join(p)] {
					if err := walk(p, sub); err != nil {
						return err
					}
				} else if err := newTable(&added, p, sub); err != nil {
					return err
				}
				continue
			}
			scalars = append(scalars, k)
		}
		if len(scalars) == 0 {
			return nil
		}
		o := owners[join(table)]
		if o == nil {
			// Only subtables declared this table; it gets a header of its own.
			sub := map[string]any{}
			for _, k := range scalars {
				sub[k] = m[k]
			}
			return newTable(&added, table, sub)
		}
		var lines strings.Builder
		for _, k := range scalars {
			line, err := renderKeyValue(slices.Concat(o.prefix, []string{k}), m[k])
			if err != nil {
				return err
			}
			lines.WriteString(line)
		}
		text := lines.String()
		if o.end > 0 && src[o.end-1] != '\n' {
			text = "\n" + text
		}
		if o == owners[""] && firstHeader >= 0 && o.end == comments(src, entries[firstHeader].start) {
			text += "\n"
		}
		edits = append(edits, tomlSplice{o.end, o.end, text})
		return nil
	}
	if err := walk(nil, want); err != nil {
		return nil, err
	}
	// An insertion goes before a removal that starts where it does, so it is not taken for part of the removed text.
	slices.SortStableFunc(edits, func(a, b tomlSplice) int {
		if a.start != b.start {
			return a.start - b.start
		}
		return (a.end - a.start) - (b.end - b.start)
	})
	// Rendered values hold no line break, so every one in the inserted text ends a line, as the file ends its own.
	nl := "\n"
	if bytes.Contains(src, []byte("\r\n")) {
		nl = "\r\n"
	}
	var b bytes.Buffer
	pos := 0
	for _, e := range edits {
		if e.start < pos {
			// Inside a table already removed.
			continue
		}
		b.Write(src[pos:e.start])
		b.WriteString(strings.ReplaceAll(e.text, "\n", nl))
		pos = e.end
	}
	b.Write(src[pos:])
	if added.Len() > 0 {
		// The edit, not src, decides whether the last line is ended: an insertion or removal at the end may have done it.
		sep := "\n"
		if !bytes.HasSuffix(b.Bytes(), []byte("\n")) {
			sep = "\n\n"
		}
		b.WriteString(strings.ReplaceAll(sep+added.String(), "\n", nl))
	}
	return b.Bytes(), nil
}

// newTable writes [path] with m's values, then a table for each table m holds.
func newTable(b *strings.Builder, path []string, m map[string]any) error {
	var lines strings.Builder
	var subtables []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if _, isMap := m[k].(map[string]any); isMap {
			subtables = append(subtables, k)
			continue
		}
		line, err := renderKeyValue([]string{k}, m[k])
		if err != nil {
			return err
		}
		lines.WriteString(line)
	}
	if lines.Len() > 0 || len(subtables) == 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("[" + renderKey(path) + "]\n" + lines.String())
	}
	for _, k := range subtables {
		if err := newTable(b, append(slices.Clone(path), k), m[k].(map[string]any)); err != nil {
			return err
		}
	}
	return nil
}

func renderKeyValue(key []string, v any) (string, error) {
	text, err := renderValue(v)
	return renderKey(key) + " = " + text + "\n", err
}

func renderKey(key []string) string {
	parts := make([]string, len(key))
	for i, k := range key {
		parts[i] = k
		if !bareKey.MatchString(k) {
			parts[i], _ = renderValue(k)
		}
	}
	return strings.Join(parts, ".")
}

// renderValue encodes v as a TOML value on one line, tables inline.
func renderValue(v any) (string, error) {
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).SetTablesInline(true).Encode(map[string]any{"v": v}); err != nil {
		return "", err
	}
	text, ok := strings.CutPrefix(strings.TrimSuffix(b.String(), "\n"), "v = ")
	if !ok || strings.Contains(text, "\n") {
		return "", fmt.Errorf("cannot write %T on one line", v)
	}
	return text, nil
}

func isTable(v any, ok bool) bool {
	_, isMap := v.(map[string]any)
	return ok && isMap
}

func isArray(v any, ok bool) bool {
	_, isSlice := v.([]any)
	return ok && isSlice
}

func same(a, b any) bool {
	switch a := a.(type) {
	case float64:
		b, ok := b.(float64)
		return ok && (a == b || math.IsNaN(a) && math.IsNaN(b))
	case []any:
		b, ok := b.([]any)
		return ok && slices.EqualFunc(a, b, same)
	case map[string]any:
		b, ok := b.(map[string]any)
		return ok && maps.EqualFunc(a, b, same)
	default:
		return reflect.DeepEqual(a, b)
	}
}

func lookup(m map[string]any, path []string) (any, bool) {
	var v any = m
	for _, k := range path {
		table, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = table[k]; !ok {
			return nil, false
		}
	}
	return v, true
}

// join keys a table; the root is "", apart from a table named "".
func join(path []string) string {
	var b strings.Builder
	for _, k := range path {
		b.WriteString("\x00" + k)
	}
	return b.String()
}

// comments returns where the comment lines right above the line at start begin.
func comments(src []byte, start int) int {
	for start > 0 {
		prev := lineStart(src, start-1)
		if !bytes.HasPrefix(bytes.TrimLeft(src[prev:start], " \t"), []byte("#")) {
			break
		}
		start = prev
	}
	return start
}

func lineStart(src []byte, pos int) int { return bytes.LastIndexByte(src[:pos], '\n') + 1 }

func lineEnd(src []byte, pos int) int {
	if n := bytes.IndexByte(src[pos:], '\n'); n >= 0 {
		return pos + n + 1
	}
	return len(src)
}
