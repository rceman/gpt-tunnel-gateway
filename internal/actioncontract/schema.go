package actioncontract

import (
	"fmt"
	"sort"
)

func (schema *CompiledSchema) JSONSchema() map[string]any {
	if schema == nil {
		return nil
	}
	result := make(map[string]any)
	if schema.Type != "" {
		result["type"] = schema.Type
	}
	if schema.Description != "" {
		result["description"] = schema.Description
	}
	if len(schema.Properties) > 0 {
		properties := make(map[string]any, len(schema.Properties))
		for _, key := range sortedCompiledKeys(schema.Properties) {
			properties[key] = schema.Properties[key].JSONSchema()
		}
		result["properties"] = properties
	}
	if len(schema.Required) > 0 {
		result["required"] = append([]string(nil), schema.Required...)
	}
	if schema.Items != nil {
		result["items"] = schema.Items.JSONSchema()
	}
	if schema.AdditionalProperties != nil {
		result["additionalProperties"] = *schema.AdditionalProperties
	} else if schema.Type == "object" {
		result["additionalProperties"] = false
	}
	if schema.AdditionalPropertySchema != nil {
		result["additionalProperties"] = schema.AdditionalPropertySchema.JSONSchema()
	}
	if len(schema.Enum) > 0 {
		values := make([]any, len(schema.Enum))
		for index, value := range schema.Enum {
			values[index] = cloneJSONValue(value)
		}
		result["enum"] = values
	}
	if schema.Pattern != "" {
		result["pattern"] = schema.Pattern
	}
	if schema.Format != "" {
		result["format"] = schema.Format
	}
	if schema.MinLength != nil {
		result["minLength"] = *schema.MinLength
	}
	if schema.MaxLength != nil {
		result["maxLength"] = *schema.MaxLength
	}
	if schema.Minimum != nil {
		result["minimum"] = *schema.Minimum
	}
	if schema.Maximum != nil {
		result["maximum"] = *schema.Maximum
	}
	if schema.MinItems != nil {
		result["minItems"] = *schema.MinItems
	}
	if schema.MaxItems != nil {
		result["maxItems"] = *schema.MaxItems
	}
	if schema.UniqueItems != nil {
		result["uniqueItems"] = *schema.UniqueItems
	}
	if schema.Unit != "" {
		result["x-unit"] = schema.Unit
	}
	if schema.HasDefault {
		result["default"] = cloneJSONValue(schema.Default)
	}
	if schema.HasConst {
		result["const"] = cloneJSONValue(schema.Const)
	}
	if len(schema.AllOf) > 0 {
		result["allOf"] = schemaListJSON(schema.AllOf)
	}
	if len(schema.OneOf) > 0 {
		result["oneOf"] = schemaListJSON(schema.OneOf)
	}
	if len(schema.AnyOf) > 0 {
		result["anyOf"] = schemaListJSON(schema.AnyOf)
	}
	if schema.Not != nil {
		result["not"] = schema.Not.JSONSchema()
	}
	if schema.If != nil {
		result["if"] = schema.If.JSONSchema()
	}
	if schema.Then != nil {
		result["then"] = schema.Then.JSONSchema()
	}
	return result
}

func schemaListJSON(schemas []*CompiledSchema) []any {
	result := make([]any, 0, len(schemas))
	for _, schema := range schemas {
		result = append(result, schema.JSONSchema())
	}
	return result
}

func sortedCompiledKeys(values map[string]*CompiledSchema) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (compiled *CompiledSet) DefinitionNames() []string {
	if compiled == nil {
		return nil
	}
	result := make([]string, 0, len(compiled.definitions))
	for name := range compiled.definitions {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func (compiled *CompiledSet) DefinitionSchema(name string) (map[string]any, bool) {
	if compiled == nil {
		return nil, false
	}
	schema, ok := compiled.definitions[name]
	if !ok || schema == nil {
		return nil, false
	}
	return schema.JSONSchema(), true
}

func (compiled *CompiledSet) Actions() []CompiledAction {
	if compiled == nil {
		return nil
	}
	result := make([]CompiledAction, 0, len(compiled.paths))
	for _, path := range compiled.paths {
		action := compiled.actions[path]
		metadata := action.Metadata
		metadata.HandlerInputMappings = append([]FieldMapping(nil), action.Metadata.HandlerInputMappings...)
		metadata.HandlerOutputMappings = append([]FieldMapping(nil), action.Metadata.HandlerOutputMappings...)
		result = append(result, CompiledAction{
			Path:        path,
			Description: action.Description,
			Metadata:    metadata,
			Input:       cloneSchema(action.Input),
			Output:      cloneSchema(action.Output),
		})
	}
	return result
}

func (compiled *CompiledSet) ActionInputSchema(path string) (map[string]any, error) {
	action, ok := compiled.actions[path]
	if !ok {
		return nil, fmt.Errorf("unknown action %q", path)
	}
	return action.Input.JSONSchema(), nil
}

func (compiled *CompiledSet) ActionOutputSchema(path string) (map[string]any, error) {
	action, ok := compiled.actions[path]
	if !ok {
		return nil, fmt.Errorf("unknown action %q", path)
	}
	return action.Output.JSONSchema(), nil
}

func (compiled *CompiledSet) CompactInputSchema(path string) (map[string]any, error) {
	action, ok := compiled.Action(path)
	if !ok {
		return nil, fmt.Errorf("unknown action %q", path)
	}
	return action.CompactInputSchema(), nil
}

func (action CompiledAction) CompactInputSchema() map[string]any {
	if action.Input == nil {
		return nil
	}
	return compactSchema(action.Input.JSONSchema()).(map[string]any)
}

func (compiled *CompiledSet) Paths() []string {
	if compiled == nil {
		return nil
	}
	return append([]string(nil), compiled.paths...)
}

func (compiled *CompiledSet) Action(path string) (CompiledAction, bool) {
	if compiled == nil {
		return CompiledAction{}, false
	}
	action, ok := compiled.actions[path]
	if !ok {
		return CompiledAction{}, false
	}
	action.Input = cloneSchema(action.Input)
	action.Output = cloneSchema(action.Output)
	action.Metadata.HandlerInputMappings = append([]FieldMapping(nil), action.Metadata.HandlerInputMappings...)
	action.Metadata.HandlerOutputMappings = append([]FieldMapping(nil), action.Metadata.HandlerOutputMappings...)
	return action, true
}

func (compiled *CompiledSet) CompactDiscovery(domain string) (Discovery, error) {
	if compiled == nil {
		return Discovery{}, fmt.Errorf("compiled contract set is nil")
	}
	if domain == "" {
		domains := make(map[string]struct{})
		for _, path := range compiled.paths {
			name, _, _ := splitActionPath(path)
			domains[name] = struct{}{}
		}
		result := Discovery{Domains: make([]string, 0, len(domains))}
		for name := range domains {
			result.Domains = append(result.Domains, name)
		}
		sort.Strings(result.Domains)
		return result, nil
	}
	if !validName(domain) {
		return Discovery{}, fmt.Errorf("invalid action domain %q", domain)
	}
	result := Discovery{Actions: make([]ActionSummary, 0)}
	for _, path := range compiled.paths {
		name, _, _ := splitActionPath(path)
		if name == domain {
			action := compiled.actions[path]
			input, _ := compiled.CompactInputSchema(path)
			result.Actions = append(result.Actions, ActionSummary{
				Path:        path,
				Description: action.Description,
				Input:       input,
			})
		}
	}
	if len(result.Actions) == 0 {
		return Discovery{}, fmt.Errorf("unknown action domain %q", domain)
	}
	return result, nil
}

func cloneJSONValue(value any) any {
	switch value := value.(type) {
	case []any:
		result := make([]any, len(value))
		for index, item := range value {
			result[index] = cloneJSONValue(item)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, item := range value {
			result[key] = cloneJSONValue(item)
		}
		return result
	default:
		return value
	}
}

func cloneSchema(schema *CompiledSchema) *CompiledSchema {
	if schema == nil {
		return nil
	}
	result := *schema
	result.Required = append([]string(nil), schema.Required...)
	result.Enum = make([]any, len(schema.Enum))
	for index, value := range schema.Enum {
		result.Enum[index] = cloneJSONValue(value)
	}
	result.Default = cloneJSONValue(schema.Default)
	result.Const = cloneJSONValue(schema.Const)
	result.AllOf = cloneSchemaList(schema.AllOf)
	result.OneOf = cloneSchemaList(schema.OneOf)
	result.AnyOf = cloneSchemaList(schema.AnyOf)
	result.Properties = make(map[string]*CompiledSchema, len(schema.Properties))
	for key, child := range schema.Properties {
		result.Properties[key] = cloneSchema(child)
	}
	result.Items = cloneSchema(schema.Items)
	result.AdditionalPropertySchema = cloneSchema(schema.AdditionalPropertySchema)
	result.Not = cloneSchema(schema.Not)
	result.If = cloneSchema(schema.If)
	result.Then = cloneSchema(schema.Then)
	result.MinLength = cloneInt(schema.MinLength)
	result.MaxLength = cloneInt(schema.MaxLength)
	result.Minimum = cloneFloat(schema.Minimum)
	result.Maximum = cloneFloat(schema.Maximum)
	result.MinItems = cloneInt(schema.MinItems)
	result.MaxItems = cloneInt(schema.MaxItems)
	result.UniqueItems = cloneBool(schema.UniqueItems)
	if schema.AdditionalProperties != nil {
		value := *schema.AdditionalProperties
		result.AdditionalProperties = &value
	}
	return &result
}

func cloneSchemaList(values []*CompiledSchema) []*CompiledSchema {
	if len(values) == 0 {
		return nil
	}
	result := make([]*CompiledSchema, len(values))
	for index, value := range values {
		result[index] = cloneSchema(value)
	}
	return result
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
