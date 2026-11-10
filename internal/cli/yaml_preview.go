// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"github.com/aiven-open/ai-gateway-cli/internal/redact"
)

var yamlCredential = regexp.MustCompile(`(?i)^` + redact.Name + `$`)

// YAML admits unquoted and block values; quoted-string redaction cannot protect its original diff side.
func (r redactor) yaml(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	file, err := parser.ParseBytes([]byte(text), parser.ParseComments)
	if err != nil {
		return "<redacted: YAML preview unavailable>\n"
	}
	// Expand aliases before rendering: the same credential may also occur at its noncredential anchor. Unmarshal
	// would decode only the first document.
	dec := yaml.NewDecoder(strings.NewReader(text))
	for {
		var expanded any
		if err := dec.Decode(&expanded); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return "<redacted: YAML preview unavailable>\n"
		}
		r.yamlValues(expanded, "")
	}
	for _, doc := range file.Docs {
		if doc.Body != nil {
			ast.Walk(yamlRedactor{r}, doc)
		}
	}
	return file.String()
}

func (r redactor) yamlValues(value any, field string) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			name := key
			if yamlCredential.MatchString(field) {
				name = field
			}
			r.yamlValues(child, name)
		}
	case []any:
		for _, child := range value {
			r.yamlValues(child, field)
		}
	case nil:
	default:
		if text := fmt.Sprint(value); yamlCredential.MatchString(field) && !r.keeps(field+":", strconv.Quote(text)) {
			r.mask(text)
		}
	}
}

type yamlRedactor struct{ redactor }

func (r yamlRedactor) Visit(node ast.Node) ast.Visitor {
	switch node := node.(type) {
	case *ast.MappingValueNode:
		node.Value = r.value(node.Key.GetToken().Value, node.Value)
	case *ast.SequenceNode:
		for i, value := range node.Values {
			node.Values[i] = r.value("", value)
		}
	}
	return r
}

func (r yamlRedactor) value(name string, node ast.Node) ast.Node {
	if value, ok := node.(*ast.StringNode); ok &&
		r.keeps(name+":", strconv.Quote(value.Value)) {
		return node
	}
	if _, empty := node.(*ast.NullNode); empty {
		return node
	}
	var value any
	if err := yaml.NodeToValue(node, &value); err != nil {
		value = node.String()
	}
	text := fmt.Sprint(value)
	if !yamlCredential.MatchString(name) && r.seen[text] == 0 {
		return node
	}
	masked := r.mask(text)
	position := *node.GetToken().Position
	replacement := ast.String(token.New(masked, masked, &position))
	replacement.GetToken().Type = token.DoubleQuoteType
	return replacement
}
