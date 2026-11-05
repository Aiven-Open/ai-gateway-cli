// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package omp

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

func nativePath(dir, name string) (string, error) {
	for _, suffix := range []string{".yml", ".yaml"} {
		path := filepath.Join(dir, name+suffix)
		if _, err := os.Lstat(path); err == nil {
			return path, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect %s: %w", path, err)
		}
	}
	return filepath.Join(dir, name+".yml"), nil
}

func readConfig(path string) (map[string]any, error) {
	data, err := fsutil.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("%s is not a YAML object; fix or move it, then retry", path)
	}
	if decoded == nil {
		return map[string]any{}, nil
	}
	cfg, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s is not a YAML object; fix or move it, then retry", path)
	}
	return cfg, nil
}

// mergeConfig changes one owned child while retaining unrelated AST nodes and their comments.
func mergeConfig(path, parent, key string, value any) ([]byte, error) {
	before, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	// Decouple YAML alias references before comparing the owned edit to unrelated values.
	copyData, err := yaml.Marshal(before)
	if err != nil {
		return nil, fmt.Errorf("copy %s: %w", path, err)
	}
	if err := yaml.Unmarshal(copyData, &before); err != nil {
		return nil, fmt.Errorf("copy %s: %w", path, err)
	}
	raw, err := fsutil.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}\n")
	} else if commentsOnly(raw) {
		raw = append(raw, []byte("\n{}\n")...)
	}
	file, err := parser.ParseBytes(raw, parser.ParseComments)
	if err != nil || len(file.Docs) != 1 {
		return nil, fmt.Errorf("%s must contain one YAML mapping; fix or move it, then retry", path)
	}
	root, ok := mappingNode(file.Docs[0].Body)
	var comment *ast.CommentGroupNode
	empty := false
	switch node := file.Docs[0].Body.(type) {
	case nil:
		empty = true
	case *ast.NullNode:
		comment, empty = node.GetComment(), true
	case *ast.CommentGroupNode:
		comment, empty = node, true
	}
	if empty {
		emptyFile, err := parser.ParseBytes([]byte("{}\n"), 0)
		if err != nil {
			return nil, fmt.Errorf("initialize %s: %w", path, err)
		}
		root = emptyFile.Docs[0].Body.(*ast.MappingNode)
		if err := root.SetComment(comment); err != nil {
			return nil, fmt.Errorf("preserve %s comment: %w", path, err)
		}
		file.Docs[0].Body, ok = root, true
	}
	if !ok {
		return nil, fmt.Errorf("%s must contain a YAML mapping; fix or move it, then retry", path)
	}
	for _, entry := range root.Values {
		if keyName(entry.Key) != parent {
			continue
		}
		mapping, ok := mappingNode(entry.Value)
		if !ok {
			return nil, fmt.Errorf("%s: %s must be a direct YAML mapping; fix or move it, then retry", path, parent)
		}
		patch, err := yaml.Marshal(map[string]any{key: value})
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", path, err)
		}
		var comment *ast.CommentGroupNode
		for _, child := range mapping.Values {
			if keyName(child.Key) == key {
				comment = child.Value.GetComment()
			}
		}
		patchFile, err := parser.ParseBytes(patch, 0)
		if err != nil {
			return nil, fmt.Errorf("merge %s: %w", path, err)
		}
		patchMapping := patchFile.Docs[0].Body.(*ast.MappingNode)
		replaced := false
		for _, child := range mapping.Values {
			if keyName(child.Key) == key {
				if err := child.Replace(patchMapping.Values[0].Value); err != nil {
					return nil, fmt.Errorf("replace %s: %w", path, err)
				}
				replaced = true
				break
			}
		}
		if !replaced {
			mapping.Merge(patchMapping)
		}
		for _, child := range mapping.Values {
			if keyName(child.Key) == key && comment != nil {
				if err := child.Value.SetComment(comment); err != nil {
					return nil, fmt.Errorf("preserve %s comment: %w", path, err)
				}
			}
		}
		mapping.IsFlowStyle = false
		return checkedYAML(path, file, before, parent, key, value)
	}
	patch, err := yaml.Marshal(map[string]any{parent: map[string]any{key: value}})
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", path, err)
	}
	patchFile, err := parser.ParseBytes(patch, 0)
	if err != nil {
		return nil, fmt.Errorf("merge %s: %w", path, err)
	}
	root.Merge(patchFile.Docs[0].Body.(*ast.MappingNode))
	root.IsFlowStyle = false
	return checkedYAML(path, file, before, parent, key, value)
}

func commentsOnly(data []byte) bool {
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) > 0 && line[0] != '#' {
			return false
		}
	}
	return true
}

func mappingNode(node ast.Node) (*ast.MappingNode, bool) {
	switch n := node.(type) {
	case *ast.MappingNode:
		return n, true
	case *ast.AnchorNode:
		return mappingNode(n.Value)
	case *ast.TagNode:
		return mappingNode(n.Value)
	}
	return nil, false
}

func keyName(node ast.MapKeyNode) string {
	if n, ok := node.(*ast.StringNode); ok {
		return n.Value
	}
	return node.String()
}

func checkedYAML(path string, file *ast.File, before map[string]any, parent, key string, value any) ([]byte, error) {
	data := []byte(file.String())
	var after map[string]any
	if err := yaml.Unmarshal(data, &after); err != nil {
		return nil, fmt.Errorf("generated %s cannot preserve YAML: %w", path, err)
	}
	owned, ok := before[parent].(map[string]any)
	if before[parent] != nil && !ok {
		return nil, fmt.Errorf("%s: %s is not a YAML mapping", path, parent)
	}
	if owned == nil {
		owned = map[string]any{}
		before[parent] = owned
	}
	owned[key] = value
	expected, err := yaml.Marshal(before)
	if err != nil {
		return nil, fmt.Errorf("encode expected %s: %w", path, err)
	}
	var normalized map[string]any
	if err := yaml.Unmarshal(expected, &normalized); err != nil {
		return nil, fmt.Errorf("decode expected %s: %w", path, err)
	}
	if !reflect.DeepEqual(after, normalized) {
		return nil, fmt.Errorf("%s: the edit would change unrelated YAML aliases; make the owned mapping independent, then retry", path)
	}
	return data, nil
}
