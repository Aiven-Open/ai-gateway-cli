// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package ompinit

import (
	"errors"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
)

// doc edits a YAML file in place through goccy's AST so comments, blank lines and layout survive.
type doc struct {
	f *ast.File
	// prefix is the original text of an empty or comment-only file, which has no mapping to merge into.
	prefix string
}

var errNotMapping = errors.New("not a mapping")

func parseDoc(data []byte) (*doc, error) {
	f, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	d := &doc{f: f}
	switch f.Docs[0].Body.(type) {
	case *ast.MappingNode:
	case nil, *ast.CommentGroupNode:
		d.prefix = string(data)
	default:
		return nil, errNotMapping
	}
	return d, nil
}

func (d *doc) String() string {
	if d.prefix == "" {
		return d.f.String()
	}
	return d.prefix + "\n" + d.f.String()
}

// set overwrites the scalar at path or creates it with any missing parents; v is a string or bool.
func (d *doc) set(path []string, v any) error {
	if _, err := pathOf(path); err != nil { // the lookups below treat an unparsable path as absent
		return err
	}
	if _, ok := d.f.Docs[0].Body.(*ast.MappingNode); !ok {
		f, err := parser.ParseBytes([]byte(snippet(path, v)), parser.ParseComments)
		d.f = f
		return err
	}
	if n := d.node(path); n != nil {
		return d.replace(path, n, v)
	}
	i := len(path) - 1
	for ; i > 0; i-- {
		if d.node(path[:i]) != nil {
			break
		}
	}
	rest := snippet(path[i:], v)
	switch parent := d.node(path[:i]).(type) {
	case *ast.MappingNode:
		if parent.IsFlowStyle {
			rest = flowSnippet(path[i:], v) // a block mapping cannot be inserted into a flow one
		}
		return d.merge(path[:i], rest)
	case *ast.NullNode: // `key:` or `key: ~` holds nothing to lose
		p, err := pathOf(path[:i])
		if err != nil {
			return err
		}
		return p.ReplaceWithReader(d.f, strings.NewReader(rest))
	default:
		return &notMappingError{path: path[:i], kind: kindOf(parent)}
	}
}

// notMappingError is a parent key that holds something the launcher will not overwrite.
type notMappingError struct {
	path []string
	kind string
}

func (e *notMappingError) Error() string {
	return e.path[len(e.path)-1] + " (" + strings.Join(e.path, ".") + ") is " + e.kind + ", not a mapping; the role keys cannot go under it"
}

func kindOf(n ast.Node) string {
	switch n.(type) {
	case *ast.AliasNode:
		return "an alias"
	case *ast.SequenceNode:
		return "a sequence"
	}
	return "a scalar"
}

// merge adds the YAML text under the mapping at path; anchors on the way are looked through.
func (d *doc) merge(path []string, text string) error {
	src, err := parser.ParseBytes([]byte(text), 0)
	if err != nil {
		return err
	}
	for _, sd := range src.Docs {
		if err := ast.Merge(d.node(path), sd); err != nil {
			return err
		}
	}
	return nil
}

// replace gives the scalar n at path the value v in place, so its anchor and comment stay; other kinds are swapped for the new text.
func (d *doc) replace(path []string, n ast.Node, v any) error {
	switch x := n.(type) {
	case *ast.StringNode:
		if s, ok := v.(string); ok {
			x.Value = s
			if strings.HasPrefix(literal(s), `"`) && x.Token.Type == token.StringType {
				x.Token.Type = token.DoubleQuoteType
			}
			return nil
		}
	case *ast.BoolNode:
		if b, ok := v.(bool); ok {
			x.Value, x.Token.Value = b, literal(b)
			return nil
		}
	}
	p, err := pathOf(path)
	if err != nil {
		return err
	}
	return p.ReplaceWithReader(d.f, strings.NewReader(literal(v)))
}

// node returns the node at path, the document body for the empty path, or nil; an anchor is looked through to its value.
func (d *doc) node(path []string) ast.Node {
	var n ast.Node = d.f.Docs[0].Body
	if len(path) > 0 {
		if p, err := pathOf(path); err == nil {
			n, _ = p.FilterFile(d.f)
		}
	}
	for {
		a, ok := n.(*ast.AnchorNode)
		if !ok {
			return n
		}
		n = a.Value
	}
}

// get returns the string or bool at path, or the node's text for any other kind.
func (d *doc) get(path []string) (any, bool) {
	n := d.node(path)
	if n == nil {
		return nil, false
	}
	switch x := n.(type) {
	case *ast.StringNode:
		return x.Value, true
	case *ast.BoolNode:
		return x.Value, true
	}
	return n.String(), true // other node kinds compare as text, so never equal a recorded alias or true
}

func pathOf(path []string) (*yaml.Path, error) {
	return yaml.PathString(strings.Join(append([]string{"$"}, path...), "."))
}

// literal is v as YAML text, quoted when a plain scalar would not read back as the same string.
func literal(v any) string {
	b, _ := yaml.Marshal(v) // v is a string or bool, which always marshal
	return strings.TrimSuffix(string(b), "\n")
}

// flowSnippet is snippet as one line of nested flow mappings.
func flowSnippet(path []string, v any) string {
	last := len(path) - 1
	return strings.Join(path[:last+1], ": {") + ": " + literal(v) + strings.Repeat("}", last) + "\n"
}

func snippet(path []string, v any) string {
	var b strings.Builder
	for i, k := range path {
		b.WriteString(strings.Repeat("  ", i) + k + ":")
		if i == len(path)-1 {
			b.WriteString(" " + literal(v))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// remove deletes the key at path, then its parent when that left it empty; only advisor blocks are ours to prune.
func (d *doc) remove(path []string) {
	d.removeKey(path)
	if len(path) < 2 {
		return
	}
	parent := path[:len(path)-1]
	if k := parent[len(parent)-1]; k != "advisor" && k != "agentAdvisor" {
		return
	}
	if m, ok := d.node(parent).(*ast.MappingNode); ok && len(m.Values) == 0 {
		d.removeKey(parent)
	}
}

func (d *doc) removeKey(path []string) {
	var parent *ast.MappingNode
	if len(path) == 1 {
		parent, _ = d.f.Docs[0].Body.(*ast.MappingNode)
	} else {
		parent, _ = d.node(path[:len(path)-1]).(*ast.MappingNode)
	}
	if parent == nil {
		return
	}
	last := path[len(path)-1]
	parent.Values = slices.DeleteFunc(parent.Values, func(mv *ast.MappingValueNode) bool { return mv.Key.GetToken().Value == last })
}
