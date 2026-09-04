package validate

import (
	"bytes"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
)

var (
	errUnsupportedClaudeAgentMetadata = errors.New("Claude plugin agent metadata is unsupported")
	errUnsupportedCodexAgentMetadata  = errors.New("Codex agent metadata is unsupported")
)

func codexAgentName(source []byte) (string, error) {
	if !utf8.Valid(source) {
		return "", errors.New("Codex agent is not valid UTF-8")
	}
	var fields map[string]any
	if err := toml.Unmarshal(source, &fields); err != nil {
		return "", errors.New("Codex agent configuration is invalid")
	}
	allowed := map[string]struct{}{
		"name": {}, "description": {}, "nickname_candidates": {}, "developer_instructions": {},
		"model": {}, "model_reasoning_effort": {}, "model_reasoning_summary": {}, "model_verbosity": {},
		"personality": {}, "service_tier": {}, "features": {},
	}
	for field := range fields {
		if _, supported := allowed[field]; !supported {
			return "", errUnsupportedCodexAgentMetadata
		}
	}
	name, nameOK := nonblankTOMLString(fields, "name")
	_, descriptionOK := nonblankTOMLString(fields, "description")
	_, instructionsOK := nonblankTOMLString(fields, "developer_instructions")
	if !nameOK || !descriptionOK || !instructionsOK {
		return "", errors.New("Codex agent configuration is invalid")
	}
	for _, field := range []string{"model", "model_reasoning_effort", "model_reasoning_summary", "model_verbosity", "personality", "service_tier"} {
		if _, present := fields[field]; present {
			if _, valid := nonblankTOMLString(fields, field); !valid {
				return "", errors.New("Codex agent configuration is invalid")
			}
		}
	}
	if candidates, present := fields["nickname_candidates"]; present && !nonblankTOMLStrings(candidates) {
		return "", errors.New("Codex agent configuration is invalid")
	}
	if features, present := fields["features"]; present && !validCodexFeatureReductions(features) {
		return "", errUnsupportedCodexAgentMetadata
	}
	return name, nil
}

func validCodexFeatureReductions(value any) bool {
	features, ok := value.(map[string]any)
	if !ok || len(features) == 0 {
		return false
	}
	allowed := map[string]struct{}{
		"shell_tool":               {},
		"apps":                     {},
		"personality":              {},
		"plugins":                  {},
		"memories":                 {},
		"request_permissions_tool": {},
	}
	for name, value := range features {
		if _, supported := allowed[name]; !supported {
			return false
		}
		enabled, boolean := value.(bool)
		if !boolean || enabled {
			return false
		}
	}
	return true
}

func nonblankTOMLString(fields map[string]any, name string) (string, bool) {
	value, ok := fields[name].(string)
	return value, ok && strings.TrimSpace(value) != ""
}

func nonblankTOMLStrings(value any) bool {
	values, ok := value.([]any)
	if !ok || len(values) == 0 {
		return false
	}
	for _, value := range values {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return false
		}
	}
	return true
}

func claudeAgentName(source []byte) (string, error) {
	if !utf8.Valid(source) {
		return "", errors.New("Claude agent is not valid UTF-8")
	}
	lines := bytes.Split(source, []byte{'\n'})
	if len(lines) < 3 || !bytes.Equal(bytes.TrimSuffix(lines[0], []byte{'\r'}), []byte("---")) {
		return "", errors.New("Claude agent frontmatter is missing")
	}
	end := -1
	for index := 1; index < len(lines); index++ {
		if bytes.Equal(bytes.TrimSuffix(lines[index], []byte{'\r'}), []byte("---")) {
			end = index
			break
		}
	}
	if end < 0 {
		return "", errors.New("Claude agent frontmatter is incomplete")
	}
	var document yaml.Node
	if err := yaml.Unmarshal(bytes.Join(lines[1:end], []byte{'\n'}), &document); err != nil || len(document.Content) != 1 {
		return "", errors.New("Claude agent frontmatter is invalid")
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", errors.New("Claude agent frontmatter must be a mapping")
	}
	seen := make(map[string]struct{}, len(root.Content)/2)
	name := ""
	description := ""
	for index := 0; index < len(root.Content); index += 2 {
		key := root.Content[index]
		value := root.Content[index+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || containsYAMLAlias(value) {
			return "", errors.New("Claude agent frontmatter is invalid")
		}
		if _, duplicate := seen[key.Value]; duplicate {
			return "", errors.New("Claude agent frontmatter contains duplicate keys")
		}
		seen[key.Value] = struct{}{}
		switch key.Value {
		case "permissionMode", "hooks", "mcpServers", "initialPrompt":
			return "", errUnsupportedClaudeAgentMetadata
		case "name":
			if !yamlString(value) {
				return "", errors.New("Claude agent name is invalid")
			}
			name = value.Value
		case "description":
			if !yamlString(value) {
				return "", errors.New("Claude agent description is invalid")
			}
			description = value.Value
		case "model", "effort", "memory", "isolation":
			if !yamlString(value) || strings.TrimSpace(value.Value) == "" {
				return "", errors.New("Claude agent frontmatter field is invalid")
			}
		case "tools", "disallowedTools", "skills":
			if !yamlStringOrStringSequence(value) {
				return "", errors.New("Claude agent frontmatter field is invalid")
			}
		case "maxTurns":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!int" {
				return "", errors.New("Claude agent frontmatter field is invalid")
			}
		case "background":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" {
				return "", errors.New("Claude agent frontmatter field is invalid")
			}
		default:
			return "", errors.New("Claude agent frontmatter contains an unknown field")
		}
	}
	if !validManifestID(name) {
		return "", errors.New("Claude agent name is missing or invalid")
	}
	if strings.TrimSpace(description) == "" {
		return "", errors.New("Claude agent description is missing or invalid")
	}
	if strings.TrimSpace(string(bytes.Join(lines[end+1:], []byte{'\n'}))) == "" {
		return "", errors.New("Claude agent instructions are missing")
	}
	return name, nil
}

func yamlString(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && node.Tag == "!!str"
}

func yamlStringOrStringSequence(node *yaml.Node) bool {
	if yamlString(node) {
		return strings.TrimSpace(node.Value) != ""
	}
	if node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
		return false
	}
	for _, child := range node.Content {
		if !yamlString(child) || strings.TrimSpace(child.Value) == "" {
			return false
		}
	}
	return true
}

func containsYAMLAlias(node *yaml.Node) bool {
	if node.Kind == yaml.AliasNode {
		return true
	}
	for _, child := range node.Content {
		if containsYAMLAlias(child) {
			return true
		}
	}
	return false
}
