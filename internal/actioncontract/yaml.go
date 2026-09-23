package actioncontract

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func decodeStrictYAML(data []byte, target any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return fmt.Errorf("YAML document is empty")
	}
	var document yaml.Node
	first := yaml.NewDecoder(bytes.NewReader(data))
	if err := first.Decode(&document); err != nil {
		return fmt.Errorf("decode YAML: %w", err)
	}
	if err := inspectYAMLNode(&document, "$"); err != nil {
		return err
	}
	var trailing yaml.Node
	if err := first.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple YAML documents are not allowed")
		}
		return fmt.Errorf("decode trailing YAML: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode YAML contract: %w", err)
	}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple YAML documents are not allowed")
		}
		return fmt.Errorf("decode trailing YAML: %w", err)
	}
	return nil
}

func inspectYAMLNode(node *yaml.Node, path string) error {
	if node == nil {
		return fmt.Errorf("%s: missing YAML node", path)
	}
	if node.Anchor != "" || node.Kind == yaml.AliasNode || node.Style&yaml.TaggedStyle != 0 {
		return fmt.Errorf("%s: YAML anchors, aliases, and explicit tags are not allowed", path)
	}
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" && !literalYAMLPath(path) {
		return fmt.Errorf("%s: null is not a valid contract declaration", path)
	}
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" && node.Value == "" && strings.Contains(path, ".metadata.") {
		return fmt.Errorf("%s: metadata values cannot be empty", path)
	}
	if node.Kind == yaml.SequenceNode && len(node.Content) == 0 && strings.HasSuffix(path, ".metadata.cross_domain_fields") {
		return fmt.Errorf("%s: metadata list cannot be empty", path)
	}
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) != 1 {
			return fmt.Errorf("%s: expected one YAML root value", path)
		}
		return inspectYAMLNode(node.Content[0], path)
	case yaml.MappingNode:
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value == "<<" {
				return fmt.Errorf("%s: mapping keys must be plain strings; merge keys are not allowed", path)
			}
			if _, exists := seen[key.Value]; exists {
				return fmt.Errorf("%s.%s: duplicate YAML key", path, key.Value)
			}
			seen[key.Value] = struct{}{}
			if err := inspectYAMLNode(value, path+"."+key.Value); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for index, child := range node.Content {
			if err := inspectYAMLNode(child, path+"["+strconv.Itoa(index)+"]"); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str", "!!null", "!!bool", "!!int", "!!float":
		default:
			return fmt.Errorf("%s: unsupported YAML scalar tag %q", path, node.Tag)
		}
	default:
		return fmt.Errorf("%s: unsupported YAML node kind %d", path, node.Kind)
	}
	return nil
}

func literalYAMLPath(path string) bool {
	for _, key := range []string{".default", ".const", ".default_override.value", ".enum"} {
		index := strings.LastIndex(path, key)
		if index < 0 {
			continue
		}
		suffix := path[index+len(key):]
		if key == ".enum" {
			return strings.HasPrefix(suffix, "[")
		}
		if suffix == "" || strings.HasPrefix(suffix, "[") || strings.HasPrefix(suffix, ".") {
			return true
		}
	}
	return false
}

func yamlValue(node *yaml.Node) (any, error) {
	if node == nil {
		return nil, fmt.Errorf("missing YAML value")
	}
	switch node.Kind {
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str":
			return node.Value, nil
		case "!!null":
			return nil, nil
		case "!!bool":
			value, err := strconv.ParseBool(node.Value)
			if err != nil {
				return nil, fmt.Errorf("invalid boolean default")
			}
			return value, nil
		case "!!int":
			var value int64
			if err := node.Decode(&value); err != nil {
				return nil, fmt.Errorf("invalid integer default: %w", err)
			}
			return value, nil
		case "!!float":
			var value float64
			if err := node.Decode(&value); err != nil {
				return nil, fmt.Errorf("invalid numeric default: %w", err)
			}
			return value, nil
		default:
			return nil, fmt.Errorf("unsupported YAML scalar tag %q", node.Tag)
		}
	case yaml.SequenceNode:
		values := make([]any, 0, len(node.Content))
		for _, child := range node.Content {
			value, err := yamlValue(child)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	case yaml.MappingNode:
		values := make(map[string]any, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			value, err := yamlValue(node.Content[index+1])
			if err != nil {
				return nil, err
			}
			values[node.Content[index].Value] = value
		}
		return values, nil
	default:
		return nil, fmt.Errorf("unsupported YAML value node kind %d", node.Kind)
	}
}
