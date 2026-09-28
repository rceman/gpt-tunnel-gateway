package actioncontract

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (compiled *CompiledSet) CompileProcedureAction(path, description, guide string, input, output map[string]any) (CompiledAction, error) {
	if compiled == nil || !strings.HasPrefix(path, "procedure/") || !namePattern.MatchString(strings.TrimPrefix(path, "procedure/")) || strings.Contains(strings.TrimPrefix(path, "procedure/"), "/") {
		return CompiledAction{}, fmt.Errorf("invalid dynamic Procedure action path")
	}
	if description == "" || utf8.RuneCountInString(description) > 256 || strings.IndexFunc(description, unicode.IsControl) >= 0 {
		return CompiledAction{}, fmt.Errorf("invalid dynamic Procedure description")
	}
	if guide == "" || utf8.RuneCountInString(guide) > 768 || !utf8.ValidString(guide) {
		return CompiledAction{}, fmt.Errorf("invalid dynamic Procedure guide")
	}
	inputSchema, err := compileProcedureSchema(compiled, input)
	if err != nil {
		return CompiledAction{}, fmt.Errorf("Procedure input: %w", err)
	}
	outputSchema, err := compileProcedureSchema(compiled, output)
	if err != nil {
		return CompiledAction{}, fmt.Errorf("Procedure output: %w", err)
	}
	if inputSchema.Type != "object" || outputSchema.Type != "object" {
		return CompiledAction{}, fmt.Errorf("dynamic Procedure schemas must have object roots")
	}
	operationSchema := cloneSchema(compiled.definitions["EntityKeyAndReference"])
	if operationSchema == nil {
		return CompiledAction{}, fmt.Errorf("EntityKeyAndReference shared definition is unavailable")
	}
	operationSchema.RefName = "EntityKeyAndReference"
	callOutput := &CompiledSchema{OneOf: []*CompiledSchema{
		procedureCallBranch(operationSchema, "accepted", nil, nil),
		procedureCallBranch(operationSchema, "running", nil, nil),
		procedureCallBranch(operationSchema, "completed", outputSchema, nil),
		procedureCallBranch(operationSchema, "failed", nil, procedureErrorSchema()),
		procedureCallBranch(operationSchema, "outcome_unknown", nil, procedureErrorSchema()),
	}}
	action := CompiledAction{
		Path:        path,
		Description: description,
		Guide:       guide,
		Metadata: ActionMetadata{
			Surface:  surfaceNormal,
			Selector: "none",
			Annotations: AnnotationHints{
				Destructive: true,
				OpenWorld:   true,
			},
		},
		Input:  inputSchema,
		Output: callOutput,
	}
	if err := validatePublicContract(path, surfaceNormal, action.Input, action.Output, compiled.crossDomainFields, true); err != nil {
		return CompiledAction{}, err
	}
	return action, nil
}

func procedureCallBranch(operation *CompiledSchema, status string, result, failure *CompiledSchema) *CompiledSchema {
	properties := map[string]*CompiledSchema{
		"operation": operation,
		"status":    {Type: "string", Enum: []any{status}},
	}
	required := []string{"operation", "status"}
	if result != nil {
		properties["result"] = result
		required = append(required, "result")
	}
	if failure != nil {
		properties["error"] = failure
		required = append(required, "error")
	}
	return &CompiledSchema{
		Type:                 "object",
		Properties:           properties,
		Required:             required,
		AdditionalProperties: boolPointer(false),
	}
}

func procedureErrorSchema() *CompiledSchema {
	minimum, maximum := 1, 2048
	return &CompiledSchema{
		Type:      "string",
		MinLength: &minimum,
		MaxLength: &maximum,
	}
}

func ValidateCompiledActionInput(action CompiledAction, value any) error {
	return validateCompiledActionValue(action.Input, value, "input")
}

func ValidateCompiledActionOutput(action CompiledAction, value any) error {
	return validateCompiledActionValue(action.Output, value, "output")
}

func validateCompiledActionValue(schema *CompiledSchema, value any, label string) error {
	normalized, err := normalizeJSON(value)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return validateCompiledSchema(schema, normalized, label)
}

func ProjectCompiledActionOutput(action CompiledAction, value any) (any, error) {
	normalized, err := normalizeJSON(value)
	if err != nil {
		return nil, err
	}
	return projectCompiledOutput(action.Output, normalized)
}

func compileProcedureSchema(compiled *CompiledSet, schema map[string]any) (*CompiledSchema, error) {
	if schema == nil {
		return nil, fmt.Errorf("schema is required")
	}
	state := procedureSchemaCompiler{compiled: compiled}
	return state.compile(schema, 1, false)
}

type procedureSchemaCompiler struct {
	compiled *CompiledSet
	nodes    int
}

func (compiler *procedureSchemaCompiler) compile(schema map[string]any, depth int, inArrayItem bool) (*CompiledSchema, error) {
	compiler.nodes++
	if depth > 8 || compiler.nodes > 256 {
		return nil, fmt.Errorf("schema exceeds structural bounds")
	}
	if reference, exists := schema["$ref"]; exists {
		name, ok := reference.(string)
		if !ok || inArrayItem {
			return nil, fmt.Errorf("invalid shared reference")
		}
		shared, ok := compiler.compiled.definitions[name]
		if !ok {
			return nil, fmt.Errorf("unknown shared reference %q", name)
		}
		if len(schema) != 1 {
			return nil, fmt.Errorf("shared reference cannot be combined with constraints")
		}
		result := cloneSchema(shared)
		result.RefName = name
		return result, nil
	}
	typ, ok := schema["type"].(string)
	if !ok {
		return nil, fmt.Errorf("schema type is required")
	}
	result := &CompiledSchema{Type: typ}
	switch typ {
	case "object":
		if !procedureSchemaKeysAllowed(schema, "type", "properties", "required", "additionalProperties") || schema["additionalProperties"] != false {
			return nil, fmt.Errorf("object schema must be closed")
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok || len(properties) > 32 {
			return nil, fmt.Errorf("invalid object properties")
		}
		result.Properties = make(map[string]*CompiledSchema, len(properties))
		for _, name := range sortedProcedureKeys(properties) {
			if !validPropertyName(name) {
				return nil, fmt.Errorf("invalid property name %q", name)
			}
			property, ok := properties[name].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("property %q must be a schema object", name)
			}
			child, err := compiler.compile(property, depth+1, false)
			if err != nil {
				return nil, fmt.Errorf("property %q: %w", name, err)
			}
			result.Properties[name] = child
		}
		result.AdditionalProperties = boolPointer(false)
		if rawRequired, exists := schema["required"]; exists {
			required, ok := procedureStringList(rawRequired)
			if !ok || len(required) > len(properties) {
				return nil, fmt.Errorf("invalid required property list")
			}
			seen := make(map[string]struct{}, len(required))
			for _, name := range required {
				if _, declared := result.Properties[name]; !declared {
					return nil, fmt.Errorf("required property %q is not declared", name)
				}
				if _, duplicate := seen[name]; duplicate {
					return nil, fmt.Errorf("duplicate required property %q", name)
				}
				seen[name] = struct{}{}
			}
			result.Required = required
		}
	case "array":
		if !procedureSchemaKeysAllowed(schema, "type", "items", "minItems", "maxItems", "uniqueItems") {
			return nil, fmt.Errorf("invalid array schema")
		}
		items, ok := schema["items"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("array items schema is required")
		}
		var err error
		result.Items, err = compiler.compile(items, depth+1, true)
		if err != nil {
			return nil, err
		}
		if result.MinItems, err = procedureIntPointer(schema, "minItems", 0, 64); err != nil {
			return nil, err
		}
		if result.MaxItems, err = procedureIntPointer(schema, "maxItems", 0, 64); err != nil {
			return nil, err
		}
		if result.MinItems != nil && result.MaxItems != nil && *result.MinItems > *result.MaxItems {
			return nil, fmt.Errorf("minItems exceeds maxItems")
		}
		if unique, exists := schema["uniqueItems"]; exists {
			if unique != true {
				return nil, fmt.Errorf("uniqueItems may only be true")
			}
			result.UniqueItems = boolPointer(true)
		}
	case "string":
		if !procedureSchemaKeysAllowed(schema, "type", "minLength", "maxLength", "enum") {
			return nil, fmt.Errorf("invalid string schema")
		}
		var err error
		if result.MinLength, err = procedureIntPointer(schema, "minLength", 0, 4096); err != nil {
			return nil, err
		}
		if result.MaxLength, err = procedureIntPointer(schema, "maxLength", 0, 4096); err != nil {
			return nil, err
		}
		if result.MinLength != nil && result.MaxLength != nil && *result.MinLength > *result.MaxLength {
			return nil, fmt.Errorf("minLength exceeds maxLength")
		}
		if values, exists := schema["enum"]; exists {
			enum, ok := values.([]any)
			if !ok || len(enum) == 0 || len(enum) > 64 {
				return nil, fmt.Errorf("invalid string enum")
			}
			result.Enum = append([]any(nil), enum...)
		}
	case "integer", "number":
		if !procedureSchemaKeysAllowed(schema, "type", "minimum", "maximum") {
			return nil, fmt.Errorf("invalid numeric schema")
		}
		var err error
		if result.Minimum, err = procedureNumberPointer(schema, "minimum"); err != nil {
			return nil, err
		}
		if result.Maximum, err = procedureNumberPointer(schema, "maximum"); err != nil {
			return nil, err
		}
		if result.Minimum != nil && result.Maximum != nil && *result.Minimum > *result.Maximum {
			return nil, fmt.Errorf("minimum exceeds maximum")
		}
		if typ == "integer" && (result.Minimum != nil && *result.Minimum != math.Trunc(*result.Minimum) || result.Maximum != nil && *result.Maximum != math.Trunc(*result.Maximum)) {
			return nil, fmt.Errorf("integer bounds must be integers")
		}
	case "boolean":
		if len(schema) != 1 {
			return nil, fmt.Errorf("invalid boolean schema")
		}
	default:
		return nil, fmt.Errorf("unsupported schema type %q", typ)
	}
	return result, nil
}

func procedureSchemaKeysAllowed(schema map[string]any, allowed ...string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		set[key] = struct{}{}
	}
	for key := range schema {
		if _, ok := set[key]; !ok {
			return false
		}
	}
	return true
}

func procedureStringList(value any) ([]string, bool) {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...), true
	case []any:
		result := make([]string, len(values))
		for index, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, false
			}
			result[index] = text
		}
		return result, true
	default:
		return nil, false
	}
}

func procedureIntPointer(schema map[string]any, key string, minimum, maximum int) (*int, error) {
	value, exists := schema[key]
	if !exists {
		return nil, nil
	}
	number, ok := procedureNumber(value)
	if !ok || number != math.Trunc(number) || number < float64(minimum) || number > float64(maximum) {
		return nil, fmt.Errorf("invalid %s", key)
	}
	result := int(number)
	return &result, nil
}

func procedureNumberPointer(schema map[string]any, key string) (*float64, error) {
	value, exists := schema[key]
	if !exists {
		return nil, nil
	}
	number, ok := procedureNumber(value)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < -9007199254740991 || number > 9007199254740991 {
		return nil, fmt.Errorf("invalid %s", key)
	}
	return &number, nil
}

func procedureNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int32:
		return float64(number), true
	case int64:
		return float64(number), true
	case uint:
		return float64(number), true
	case uint32:
		return float64(number), true
	case uint64:
		return float64(number), true
	case float32:
		return float64(number), true
	case float64:
		return number, true
	default:
		return 0, false
	}
}

func sortedProcedureKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func boolPointer(value bool) *bool {
	return &value
}
