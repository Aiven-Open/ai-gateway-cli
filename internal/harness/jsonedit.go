// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
)

var errSyntax = errors.New("not a JSON object")

// Edit returns src, the JSON or JSONC object at path, changed to decode as obj. Only members whose values differ are
// rewritten in place, removed, or added at the end of their object, so comments, order and layout elsewhere stay. An
// empty src, or one written on a single line without comments, has no layout to keep and gives JSON(obj).
func Edit(path string, src []byte, obj map[string]any) ([]byte, error) {
	want, err := decodeJSON(JSON(obj))
	if err != nil {
		return nil, err
	}
	start := len(src) - len(bytes.TrimPrefix(src, []byte("\xef\xbb\xbf")))
	if len(bytes.TrimSpace(src[start:])) == 0 {
		return JSON(obj), nil
	}
	have, err := decodeJSON(src)
	if _, ok := have.(map[string]any); err != nil || !ok {
		return nil, fmt.Errorf("%s is not a JSON object; fix or move it, then retry", path)
	}
	p := jsonParser{src: src, pos: start}
	p.space()
	root, err := p.value()
	if p.space(); err != nil || root.kind != '{' || p.pos != len(src) {
		return nil, fmt.Errorf("%s is not a JSON object; fix or move it, then retry", path)
	}
	if !p.comment && !bytes.ContainsRune(bytes.TrimSpace(src[start:]), '\n') {
		return slices.Concat(src[:start], JSON(obj)), nil
	}
	e := jsonEditor{src: src, unit: "  ", nl: "\n"}
	if bytes.Contains(src, []byte("\r\n")) {
		e.nl = "\r\n"
	}
	if len(root.members) > 0 {
		if inner, outer := lineIndent(src, root.members[0].start), lineIndent(src, root.start); len(inner) > len(outer) &&
			bytes.ContainsAny(src[root.start:root.members[0].start], "\n") {
			e.unit = inner[len(outer):]
		}
	}
	text, err := e.object(root, want.(map[string]any), lineIndent(src, root.start))
	if err != nil {
		return nil, fmt.Errorf("%s %w; fix or move it, then retry", path, err)
	}
	out := slices.Concat(src[:root.start], text, src[root.end:])
	// The edit must say exactly what obj says; anything else is a defect here, and the file is left alone.
	if got, err := decodeJSON(out); err != nil || !reflect.DeepEqual(got, want) {
		return nil, fmt.Errorf("could not edit %s in place; move it, then retry", path)
	}
	return out, nil
}

// decodeJSON decodes one JSON or JSONC value, keeping numbers verbatim.
func decodeJSON(data []byte) (any, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(stripJSONC(data)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errSyntax
	}
	return v, nil
}

type jsonNode struct {
	// kind is '{' or '[' for a container, 0 for a scalar.
	kind       byte
	start, end int
	members    []jsonMember
}

type jsonMember struct {
	key string
	// start is the key's opening quote; comma is the comma after the value, or -1.
	start, comma int
	value        jsonNode
}

// jsonParser finds where each value of a JSONC document is, with the syntax stripJSONC accepts.
type jsonParser struct {
	src []byte
	pos int
	// comment reports whether space skipped a comment.
	comment bool
}

func (p *jsonParser) space() {
	for p.pos < len(p.src) {
		rest := p.src[p.pos:]
		switch {
		case rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\n' || rest[0] == '\r':
			p.pos++
		case bytes.HasPrefix(rest, []byte("//")):
			p.comment = true
			if n := bytes.IndexByte(rest, '\n'); n >= 0 {
				p.pos += n
			} else {
				p.pos = len(p.src)
			}
		case bytes.HasPrefix(rest, []byte("/*")):
			p.comment = true
			n := bytes.Index(rest[2:], []byte("*/"))
			if n < 0 {
				return
			}
			p.pos += n + 4
		default:
			return
		}
	}
}

func (p *jsonParser) value() (jsonNode, error) {
	n := jsonNode{start: p.pos}
	if p.pos >= len(p.src) {
		return n, errSyntax
	}
	switch c := p.src[p.pos]; c {
	case '{', '[':
		n.kind, p.pos = c, p.pos+1
		end := byte(']')
		if c == '{' {
			end = '}'
		}
		for count := 0; ; count++ {
			if p.space(); p.pos >= len(p.src) || p.src[p.pos] == end {
				break
			}
			// stripJSONC drops one comma before the closing bracket, even with nothing before it.
			if p.src[p.pos] == ',' && count == 0 {
				p.pos++
				if p.space(); p.pos >= len(p.src) || p.src[p.pos] != end {
					return n, errSyntax
				}
				break
			}
			m := jsonMember{start: p.pos, comma: -1}
			if c == '{' {
				key, err := p.string()
				if err != nil {
					return n, err
				}
				if p.space(); p.pos >= len(p.src) || p.src[p.pos] != ':' {
					return n, errSyntax
				}
				p.pos++
				p.space()
				m.key = key
			}
			v, err := p.value()
			if err != nil {
				return n, err
			}
			if c == '{' {
				m.value = v
				n.members = append(n.members, m)
			}
			if p.space(); p.pos < len(p.src) && p.src[p.pos] == ',' {
				if c == '{' {
					n.members[len(n.members)-1].comma = p.pos
				}
				p.pos++
			} else if p.pos >= len(p.src) || p.src[p.pos] != end {
				return n, errSyntax
			}
		}
		if p.pos >= len(p.src) {
			return n, errSyntax
		}
		p.pos++
	case '"':
		if _, err := p.string(); err != nil {
			return n, err
		}
	default:
		for p.pos < len(p.src) && !strings.ContainsRune(" \t\r\n,:[]{}\"/", rune(p.src[p.pos])) {
			p.pos++
		}
		if p.pos == n.start {
			return n, errSyntax
		}
	}
	n.end = p.pos
	return n, nil
}

func (p *jsonParser) string() (string, error) {
	start := p.pos
	if p.pos >= len(p.src) || p.src[p.pos] != '"' {
		return "", errSyntax
	}
	for p.pos++; p.pos < len(p.src) && p.src[p.pos] != '"'; p.pos++ {
		if p.src[p.pos] == '\\' {
			p.pos++
		}
	}
	if p.pos >= len(p.src) {
		return "", errSyntax
	}
	p.pos++
	var s string
	if err := json.Unmarshal(p.src[start:p.pos], &s); err != nil {
		return "", errSyntax
	}
	return s, nil
}

type jsonEditor struct {
	src []byte
	// unit is one level of the file's indentation.
	unit string
	// nl ends the lines it writes, as the file ends its own.
	nl string
}

// object renders n changed to decode as want. indent is that of the line n opens on. A member owns the lines above
// it back to the previous member's line, and the rest of its own line after its comma, so removing one removes its
// comments too.
func (e jsonEditor) object(n jsonNode, want map[string]any, indent string) ([]byte, error) {
	seen := map[string]bool{}
	for _, m := range n.members {
		if seen[m.key] {
			return nil, fmt.Errorf("repeats the key %q", m.key)
		}
		seen[m.key] = true
	}
	var added []string
	for key := range want {
		if !seen[key] {
			added = append(added, key)
		}
	}
	slices.Sort(added)
	var kept []int
	for i, m := range n.members {
		if _, ok := want[m.key]; ok {
			kept = append(kept, i)
		}
	}
	if len(kept) == 0 && len(added) == 0 {
		if len(n.members) == 0 {
			return e.src[n.start:n.end], nil
		}
		return []byte("{}"), nil
	}
	inner := indent + e.unit
	if len(n.members) > 0 && bytes.ContainsRune(e.src[n.start:n.members[0].start], '\n') {
		inner = lineIndent(e.src, n.members[0].start)
	}
	if len(kept) == 0 {
		// Nothing to align with: the new members go one per line.
		var b strings.Builder
		b.WriteByte('{')
		if len(n.members) == 0 {
			// An empty object's comments belong to no member: they stay, ahead of the new members.
			b.Write(bytes.TrimRight(e.src[n.start+1:n.end-1], " \t\r\n"))
		}
		for i, key := range added {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(e.nl + inner + e.member(key, want[key], inner, false))
		}
		b.WriteString(e.nl + indent + "}")
		return []byte(b.String()), nil
	}
	last := n.members[len(n.members)-1]
	trailing := last.comma >= 0
	lead := e.src[n.start+1 : n.members[0].start]
	inline := !bytes.ContainsRune(lead, '\n')
	// tail ends a member: its comma and the rest of that line, or the spaces after the comma when the next member
	// follows on the same line. Before the closing brace, the spaces or the line end the last member's tail.
	tail := func(i int) []byte {
		m := n.members[i]
		end := n.end - 1
		if i+1 < len(n.members) {
			end = n.members[i+1].start
		}
		from := m.value.end
		if m.comma >= 0 {
			from = m.comma + 1
		}
		if nl := lineEnd(e.src[from:end]); nl >= 0 {
			return e.src[m.value.end : from+nl]
		}
		return e.src[m.value.end : from+len(e.src[from:end])-len(bytes.TrimLeft(e.src[from:end], " \t"))]
	}
	head := func(i int) []byte {
		if i == 0 {
			if nl := lineEnd(lead); nl >= 0 {
				return lead[nl:]
			}
			return lead
		}
		prev := n.members[i-1]
		return e.src[prev.value.end+len(tail(i-1)) : n.members[i].start]
	}
	var b bytes.Buffer
	b.WriteByte('{')
	if nl := lineEnd(lead); nl >= 0 {
		b.Write(lead[:nl])
	}
	for j, i := range kept {
		m := n.members[i]
		// A member that shared its line with a removed one now opens the line, so it takes the line's indentation.
		if i > 0 && !bytes.HasSuffix(tail(i-1), []byte("\n")) && bytes.HasSuffix(b.Bytes(), []byte("\n")) {
			b.WriteString(lineIndent(e.src, m.start))
		}
		b.Write(head(i))
		b.Write(e.src[m.start:m.value.start])
		text, err := e.value(m.value, want[m.key], lineIndent(e.src, m.start))
		if err != nil {
			return nil, err
		}
		b.Write(text)
		t := tail(i)
		need := j < len(kept)-1 || len(added) > 0 || trailing
		switch {
		case m.comma >= 0 && !need:
			c := m.comma - m.value.end
			if t = slices.Concat(t[:c], t[c+1:]); !bytes.ContainsRune(t, '\n') {
				t = bytes.TrimRight(t, " \t")
			}
		case m.comma < 0 && need:
			t = slices.Concat([]byte(","), t)
		}
		b.Write(t)
	}
	closing := e.src[last.value.end+len(tail(len(n.members)-1)) : n.end-1]
	for j, key := range added {
		comma := ""
		if j < len(added)-1 || trailing {
			comma = ","
		}
		switch {
		case inline:
			if !bytes.HasSuffix(b.Bytes(), []byte(" ")) {
				b.WriteByte(' ')
			}
			b.WriteString(e.member(key, want[key], inner, true) + comma)
		default:
			e.breakLine(&b)
			b.WriteString(inner + e.member(key, want[key], inner, false) + comma + e.nl)
		}
	}
	if lastTail := tail(len(n.members) - 1); len(added) > 0 && !inline && !bytes.HasSuffix(lastTail, []byte("\n")) {
		closing = []byte(indent)
	} else if bytes.HasSuffix(lastTail, []byte("\n")) {
		// The closing brace stays on its own line when the member that ended the line above it is removed.
		e.breakLine(&b)
	}
	b.Write(closing)
	b.WriteByte('}')
	return b.Bytes(), nil
}

// value renders n changed to decode as want, recursing into an object so its untouched members keep their text.
func (e jsonEditor) value(n jsonNode, want any, indent string) ([]byte, error) {
	if have, err := decodeJSON(e.src[n.start:n.end]); err == nil && reflect.DeepEqual(have, want) {
		return e.src[n.start:n.end], nil
	}
	if w, ok := want.(map[string]any); ok && n.kind == '{' {
		return e.object(n, w, indent)
	}
	return []byte(e.render(want, indent, false)), nil
}

func (e jsonEditor) member(key string, v any, indent string, inline bool) string {
	return e.render(key, indent, true) + ": " + e.render(v, indent, inline)
}

// render encodes v on lines indented by indent, or on one line when inline.
func (e jsonEditor) render(v any, indent string, inline bool) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if !inline {
		enc.SetIndent(indent, e.unit)
	}
	_ = enc.Encode(v) // decoded JSON values cannot fail to marshal
	return strings.ReplaceAll(strings.TrimSuffix(b.String(), "\n"), "\n", e.nl)
}

// breakLine ends the line b ends on, without trailing spaces, unless b already ends a line.
func (e jsonEditor) breakLine(b *bytes.Buffer) {
	if !bytes.HasSuffix(b.Bytes(), []byte("\n")) {
		b.Truncate(len(bytes.TrimRight(b.Bytes(), " \t")))
		b.WriteString(e.nl)
	}
}

// lineEnd returns the index just past the first line break in s that is not inside a block comment, or -1. s holds
// only what may separate JSON tokens.
func lineEnd(s []byte) int {
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\n':
			return i + 1
		case bytes.HasPrefix(s[i:], []byte("//")):
			if n := bytes.IndexByte(s[i:], '\n'); n >= 0 {
				return i + n + 1
			}
			return -1
		case bytes.HasPrefix(s[i:], []byte("/*")):
			n := bytes.Index(s[i+2:], []byte("*/"))
			if n < 0 {
				return -1
			}
			i += n + 3
		}
	}
	return -1
}

// lineIndent returns the spaces and tabs that open the line holding src[pos].
func lineIndent(src []byte, pos int) string {
	start := bytes.LastIndexByte(src[:pos], '\n') + 1
	end := start
	for end < pos && (src[end] == ' ' || src[end] == '\t') {
		end++
	}
	return string(src[start:end])
}

// Syntax is JSONC that a tool's own JSON reader may reject.
type Syntax uint8

const (
	LineComments Syntax = 1 << iota
	BlockComments
	TrailingCommas
	// Strict is everything JSON.parse rejects.
	Strict = LineComments | BlockComments | TrailingCommas
)

// Strip removes s from src, outside strings, so a tool whose reader rejects it reads the file setup writes; Edit keeps
// whatever src holds. A comment alone on its lines goes with them, one opening a line with the space after it, and any
// other with the space before it. An unterminated block comment stays, for decoding to refuse.
func Strip(src []byte, s Syntax) []byte {
	out := make([]byte, 0, len(src))
	bom := len(src) - len(bytes.TrimPrefix(src, []byte("\xef\xbb\xbf")))
	for i, inStr := 0, false; i < len(src); i++ {
		c := src[i]
		switch {
		case inStr && c == '\\' && i+1 < len(src):
			out = append(out, c, src[i+1])
			i++
			continue
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '/' && i+1 < len(src) && (src[i+1] == '/' || src[i+1] == '*'):
			end := len(src)
			if src[i+1] == '/' {
				if n := bytes.IndexByte(src[i:], '\n'); n >= 0 {
					end = i + n
				}
				end -= bytes.Count(src[end-1:end], []byte("\r"))
			} else if n := bytes.Index(src[i+2:], []byte("*/")); n >= 0 {
				end = i + n + 4
			} else {
				return append(out, src[i:]...)
			}
			if src[i+1] == '/' && s&LineComments == 0 || src[i+1] == '*' && s&BlockComments == 0 {
				out = append(out, src[i:end]...)
				i = end - 1
				continue
			}
			kept := len(bytes.TrimRight(out, " \t"))
			rest := end + len(src[end:]) - len(bytes.TrimLeft(src[end:], " \t"))
			first, last := kept == bom || out[kept-1] == '\n', rest == len(src) || src[rest] == '\n' || src[rest] == '\r'
			switch {
			case first && last:
				out, end = out[:kept], rest
				if end < len(src) && src[end] == '\r' {
					end++
				}
				if end < len(src) && src[end] == '\n' {
					end++
				}
			case first:
				end = rest
			default:
				out = out[:kept]
			}
			i = end - 1
			continue
		case c == ',' && s&TrailingCommas != 0:
			if j := nextToken(src[i+1:]); j >= 0 && (src[i+1+j] == '}' || src[i+1+j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// nextToken returns the index of the first byte of s outside space and comments, or -1.
func nextToken(s []byte) int {
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r':
		case bytes.HasPrefix(s[i:], []byte("//")):
			n := bytes.IndexByte(s[i:], '\n')
			if n < 0 {
				return -1
			}
			i += n
		case bytes.HasPrefix(s[i:], []byte("/*")):
			n := bytes.Index(s[i+2:], []byte("*/"))
			if n < 0 {
				return -1
			}
			i += n + 3
		default:
			return i
		}
	}
	return -1
}
