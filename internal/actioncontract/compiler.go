package actioncontract

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

var requiredDefinitions = []string{
	"WorkflowRole", "EntityKeyAndReference", "Track", "Milestone", "Revision",
	"CursorAndCompactHandle", "GitFingerprint", "NonGitDigestOrHandle", "MutationReason",
	"Timestamp", "Duration", "TrackReviewSnapshot", "TaskExecution", "TaskVerification", "Operation",
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var actionPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func Compile(sharedYAML, actionsYAML []byte) (*CompiledSet, error) {
	var shared sharedFile
	if err := decodeStrictYAML(sharedYAML, &shared); err != nil {
		return nil, fmt.Errorf("shared definitions: %w", err)
	}
	var catalog catalogSpec
	if err := decodeStrictYAML(actionsYAML, &catalog); err != nil {
		return nil, fmt.Errorf("action catalog: %w", err)
	}
	return compileSpecs(shared, catalog)
}

func CompileFiles(sharedPath, actionsPath string) (*CompiledSet, error) {
	sharedYAML, err := os.ReadFile(sharedPath)
	if err != nil {
		return nil, fmt.Errorf("read shared definitions: %w", err)
	}
	actionsYAML, err := os.ReadFile(actionsPath)
	if err != nil {
		return nil, fmt.Errorf("read action catalog: %w", err)
	}
	return Compile(sharedYAML, actionsYAML)
}

func compileSpecs(shared sharedFile, catalog catalogSpec) (*CompiledSet, error) {
	if shared.Version != catalogVersion {
		return nil, fmt.Errorf("shared definitions: unsupported version %d", shared.Version)
	}
	if catalog.Version != catalogVersion {
		return nil, fmt.Errorf("action catalog: unsupported version %d", catalog.Version)
	}
	if len(shared.Definitions) == 0 {
		return nil, fmt.Errorf("shared definitions: registry is empty")
	}
	if err := validateRequiredDefinitionNames(shared.Definitions); err != nil {
		return nil, err
	}
	for _, name := range sortedDefinitionNames(shared.Definitions) {
		if !containsString(requiredDefinitions, name) {
			return nil, fmt.Errorf("shared definitions: non-canonical definition %q is not allowed", name)
		}
	}
	for _, name := range sortedDefinitionNames(shared.Definitions) {
		definition := shared.Definitions[name]
		if !validDefinitionName(name) {
			return nil, fmt.Errorf("shared definitions: invalid name %q", name)
		}
		if definition.Schema == nil {
			return nil, fmt.Errorf("shared definitions.%s: schema is required", name)
		}
		if definition.Metadata == nil || !validateMetadataText(definition.Metadata.Kind) {
			return nil, fmt.Errorf("shared definitions.%s: metadata.kind is required", name)
		}
		if err := validateDefinitionMetadata(name, *definition.Metadata); err != nil {
			return nil, err
		}
	}
	resolver := &definitionResolver{
		specs:    shared.Definitions,
		compiled: make(map[string]*CompiledSchema, len(shared.Definitions)),
		metadata: make(map[string]definitionMetadata, len(shared.Definitions)),
		visiting: make(map[string]bool, len(shared.Definitions)),
	}
	for _, name := range sortedDefinitionNames(shared.Definitions) {
		if _, err := resolver.resolve(name, 0); err != nil {
			return nil, err
		}
		resolver.metadata[name] = *shared.Definitions[name].Metadata
	}
	if err := validateTimestampDefinition(resolver.compiled, resolver.metadata); err != nil {
		return nil, err
	}
	crossDomainFields := resolver.metadata["EntityKeyAndReference"].CrossDomainFields
	for _, name := range sortedDefinitionNames(shared.Definitions) {
		if name == "Timestamp" {
			continue
		}
		if err := validateActionSchemaFields("shared definitions."+name, "output", resolver.compiled[name], surfaceNormal, crossDomainFields, map[string]bool{}); err != nil {
			return nil, err
		}
	}
	compiled := &CompiledSet{
		definitions:        resolver.compiled,
		definitionMetadata: resolver.metadata,
		crossDomainFields:  append([]string(nil), resolver.metadata["EntityKeyAndReference"].CrossDomainFields...),
		actions:            make(map[string]CompiledAction),
	}
	domains := make(map[string]struct{}, len(catalog.Domains))
	for index, domain := range catalog.Domains {
		where := fmt.Sprintf("action catalog.domains[%d]", index)
		if !validName(domain.Name) {
			return nil, fmt.Errorf("%s: invalid domain name %q", where, domain.Name)
		}
		if isRetiredDomain(domain.Name) {
			return nil, fmt.Errorf("%s: retired action domain %q is not allowed", where, domain.Name)
		}
		if _, exists := domains[domain.Name]; exists {
			return nil, fmt.Errorf("%s: duplicate domain %q", where, domain.Name)
		}
		domains[domain.Name] = struct{}{}
		for actionIndex, action := range domain.Actions {
			path := domain.Name + "/" + action.Name
			if !actionPattern.MatchString(action.Name) {
				return nil, fmt.Errorf("%s.actions[%d]: invalid action name %q", where, actionIndex, action.Name)
			}
			if !canonicalPath(path) {
				return nil, fmt.Errorf("%s: retired action %q is not allowed", where, path)
			}
			if _, exists := compiled.actions[path]; exists {
				return nil, fmt.Errorf("action catalog: duplicate action path %q", path)
			}
			compiledAction, err := resolver.compileAction(path, action)
			if err != nil {
				return nil, err
			}
			compiled.actions[path] = compiledAction
			compiled.paths = append(compiled.paths, path)
		}
	}
	sort.Strings(compiled.paths)
	return compiled, nil
}

type definitionResolver struct {
	specs    map[string]definitionSpec
	compiled map[string]*CompiledSchema
	metadata map[string]definitionMetadata
	visiting map[string]bool
}

func (resolver *definitionResolver) resolve(name string, depth int) (*CompiledSchema, error) {
	if depth > 64 {
		return nil, fmt.Errorf("shared definition %q exceeds maximum reference depth", name)
	}
	if schema, ok := resolver.compiled[name]; ok {
		return schema, nil
	}
	spec, ok := resolver.specs[name]
	if !ok {
		return nil, fmt.Errorf("unknown shared definition %q", name)
	}
	if resolver.visiting[name] {
		return nil, fmt.Errorf("shared definition %q participates in a reference cycle", name)
	}
	resolver.visiting[name] = true
	allowTimestampFormat := name == "Timestamp"
	schema, err := resolver.compileSchema(spec.Schema, "shared definitions."+name+".schema", schemaModeShared, allowTimestampFormat, depth+1)
	delete(resolver.visiting, name)
	if err != nil {
		return nil, err
	}
	resolver.compiled[name] = schema
	return schema, nil
}

func (resolver *definitionResolver) compileAction(path string, spec actionSpec) (CompiledAction, error) {
	description := strings.TrimSpace(spec.Description)
	if description == "" || utf8.RuneCountInString(description) > 512 || strings.IndexFunc(spec.Description, unicode.IsControl) >= 0 {
		return CompiledAction{}, fmt.Errorf("action %s: description must be one bounded non-empty line", path)
	}
	if spec.Metadata == nil {
		return CompiledAction{}, fmt.Errorf("action %s: metadata is required", path)
	}
	metadata, err := compileMetadata(path, *spec.Metadata)
	if err != nil {
		return CompiledAction{}, err
	}
	if spec.Input == nil || spec.Output == nil {
		return CompiledAction{}, fmt.Errorf("action %s: input and output contracts are required", path)
	}
	input, err := resolver.compileSchema(spec.Input, "action "+path+".input", schemaModeInput, false, 0)
	if err != nil {
		return CompiledAction{}, err
	}
	output, err := resolver.compileSchema(spec.Output, "action "+path+".output", schemaModeOutput, false, 0)
	if err != nil {
		return CompiledAction{}, err
	}
	if !rootSchemaCanBeObject(input) || !rootSchemaCanBeObject(output) {
		return CompiledAction{}, fmt.Errorf("action %s: input and output roots must be objects", path)
	}
	key, hasKey := input.Properties["key"]
	if metadata.Selector == "key" && (!hasKey || !containsString(input.Required, "key") || key.RefName != "EntityKeyAndReference") {
		return CompiledAction{}, fmt.Errorf("action %s: selector key must be required and reference EntityKeyAndReference", path)
	}
	if metadata.Selector == "none" && hasKey {
		return CompiledAction{}, fmt.Errorf("action %s: top-level key requires selector metadata", path)
	}
	if path == "operation/read" || path == "operation/await" {
		if metadata.Selector != "key" {
			return CompiledAction{}, fmt.Errorf("action %s: Operation selector must be key", path)
		}
		for _, alias := range []string{"operation_id", "operation", "id"} {
			if _, exists := input.Properties[alias]; exists {
				return CompiledAction{}, fmt.Errorf("action %s: unapproved Operation selector alias %q", path, alias)
			}
		}
	}
	if err := validateDurationUnits(input, path+".input"); err != nil {
		return CompiledAction{}, err
	}
	if err := validateDurationUnits(output, path+".output"); err != nil {
		return CompiledAction{}, err
	}
	if err := validatePublicContract(path, metadata.Surface, input, output, resolver.metadata["EntityKeyAndReference"].CrossDomainFields, metadata.Annotations.OpenWorld); err != nil {
		return CompiledAction{}, err
	}
	return CompiledAction{
		Path:        path,
		Description: strings.TrimSpace(spec.Description),
		Metadata:    metadata,
		Input:       input,
		Output:      output,
	}, nil
}

func (resolver *definitionResolver) compileSchema(spec *schemaSpec, path string, mode schemaMode, allowTimestampFormat bool, depth int) (*CompiledSchema, error) {
	if spec == nil {
		return nil, fmt.Errorf("%s: schema is required", path)
	}
	if depth > 64 {
		return nil, fmt.Errorf("%s: schema exceeds maximum nesting depth", path)
	}
	if spec.Ref != "" {
		if hasRefStructuralFields(spec) {
			return nil, fmt.Errorf("%s: a shared ref cannot replace its structural schema", path)
		}
		base, err := resolver.resolve(spec.Ref, depth+1)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		compiled := cloneSchema(base)
		compiled.RefName = spec.Ref
		if spec.Description != "" {
			compiled.Description = spec.Description
		}
		if spec.Unit != "" {
			if spec.Ref != "Duration" || !validDurationUnit(spec.Unit) {
				return nil, fmt.Errorf("%s: unit metadata is only valid on a Duration reference", path)
			}
			compiled.Unit = spec.Unit
		}
		if err := mergeRefConstraints(compiled, spec, path); err != nil {
			return nil, err
		}
		if spec.Pattern != "" {
			if compiled.Pattern != "" {
				return nil, fmt.Errorf("%s: a ref cannot replace a shared pattern", path)
			}
			compiled.Pattern = spec.Pattern
		}
		if spec.Enum != nil {
			if len(*spec.Enum) == 0 {
				return nil, fmt.Errorf("%s: enum cannot be empty", path)
			}
			for index, candidate := range *spec.Enum {
				if !compiledTypeMatches(compiled.Type, candidate) {
					return nil, fmt.Errorf("%s.enum[%d]: value has wrong type", path, index)
				}
				if len(compiled.Enum) > 0 && !containsSchemaValue(compiled.Enum, candidate) {
					return nil, fmt.Errorf("%s.enum[%d]: value is outside the shared enum", path, index)
				}
				for previous := 0; previous < index; previous++ {
					if schemaEqual(candidate, (*spec.Enum)[previous]) {
						return nil, fmt.Errorf("%s.enum[%d]: duplicate value", path, index)
					}
				}
			}
			compiled.Enum = append([]any(nil), (*spec.Enum)...)
		}
		if spec.Const.Kind != 0 {
			value, err := yamlValue(&spec.Const)
			if err != nil {
				return nil, fmt.Errorf("%s.const: %w", path, err)
			}
			if !compiledTypeMatches(compiled.Type, value) || compiled.HasConst && !schemaEqual(compiled.Const, value) || len(compiled.Enum) > 0 && !containsSchemaValue(compiled.Enum, value) {
				return nil, fmt.Errorf("%s.const: value conflicts with the shared schema", path)
			}
			compiled.Const, compiled.HasConst = value, true
		}
		if spec.Default.Kind != 0 {
			if mode == schemaModeOutput {
				return nil, fmt.Errorf("%s: defaults are not allowed in output contracts", path)
			}
			if compiled.HasDefault {
				return nil, fmt.Errorf("%s.default: use default_override to replace a shared default", path)
			}
			value, err := yamlValue(&spec.Default)
			if err != nil {
				return nil, fmt.Errorf("%s.default: %w", path, err)
			}
			compiled.Default, compiled.HasDefault = value, true
		}
		if err := validateConstraintKinds(compiled, path); err != nil {
			return nil, err
		}
		if err := validateSchemaLiterals(compiled, path); err != nil {
			return nil, err
		}
		if compiled.HasDefault {
			if err := validateCompiledSchema(compiled, compiled.Default, path+".default"); err != nil {
				return nil, fmt.Errorf("%s.default: %w", path, err)
			}
		}
		if spec.DefaultOverride != nil {
			if mode == schemaModeOutput {
				return nil, fmt.Errorf("%s: defaults are not allowed in output contracts", path)
			}
			if err := applyDefaultOverride(compiled, spec.DefaultOverride, path); err != nil {
				return nil, err
			}
		}
		return compiled, nil
	}
	if spec.Unit != "" {
		return nil, fmt.Errorf("%s: unit metadata requires a Duration reference", path)
	}
	if spec.Type == "" && (spec.AllOf == nil || len(*spec.AllOf) == 0) && (spec.OneOf == nil || len(*spec.OneOf) == 0) && (spec.AnyOf == nil || len(*spec.AnyOf) == 0) && spec.Not == nil && spec.If == nil {
		return nil, fmt.Errorf("%s: type, ref, or composition is required", path)
	}
	if spec.Type != "" && !validType(spec.Type) {
		return nil, fmt.Errorf("%s: invalid schema type %q", path, spec.Type)
	}
	if spec.Format != "" && (!allowTimestampFormat || spec.Format != formatTimestamp || spec.Type != "string") {
		return nil, fmt.Errorf("%s: unsupported or unapproved format %q", path, spec.Format)
	}
	if mode == schemaModeOutput && (spec.Default.Kind != 0 || spec.DefaultOverride != nil) {
		return nil, fmt.Errorf("%s: defaults are not allowed in output contracts", path)
	}
	compiled := &CompiledSchema{
		Type:                 spec.Type,
		Description:          spec.Description,
		Pattern:              spec.Pattern,
		Format:               spec.Format,
		MinLength:            cloneInt(spec.MinLength),
		MaxLength:            cloneInt(spec.MaxLength),
		Minimum:              cloneFloat(spec.Minimum),
		Maximum:              cloneFloat(spec.Maximum),
		MinItems:             cloneInt(spec.MinItems),
		MaxItems:             cloneInt(spec.MaxItems),
		UniqueItems:          cloneBool(spec.UniqueItems),
		AdditionalProperties: cloneBool(spec.AdditionalProperties),
	}
	if spec.Type == "object" && compiled.AdditionalProperties == nil && spec.AdditionalPropertySchema == nil {
		value := false
		compiled.AdditionalProperties = &value
	}
	if spec.Required != nil && len(*spec.Required) == 0 {
		return nil, fmt.Errorf("%s: required cannot be empty", path)
	}
	if spec.Type == "object" {
		compiled.Properties = make(map[string]*CompiledSchema, len(spec.Properties))
		for _, key := range sortedSchemaKeys(spec.Properties) {
			if !validPropertyName(key) {
				return nil, fmt.Errorf("%s.properties: invalid property name %q", path, key)
			}
			propertySpec := spec.Properties[key]
			child, err := resolver.compileSchema(&propertySpec, path+".properties."+key, mode, false, depth+1)
			if err != nil {
				return nil, err
			}
			compiled.Properties[key] = child
		}
		if spec.Required != nil {
			seen := make(map[string]struct{}, len(*spec.Required))
			for _, required := range *spec.Required {
				if _, exists := seen[required]; exists {
					return nil, fmt.Errorf("%s.required: duplicate property %q", path, required)
				}
				seen[required] = struct{}{}
				property, exists := compiled.Properties[required]
				if !exists {
					return nil, fmt.Errorf("%s.required: property %q is not declared", path, required)
				}
				if property.HasDefault {
					return nil, fmt.Errorf("%s.required: property %q cannot also have a default", path, required)
				}
				compiled.Required = append(compiled.Required, required)
			}
			sort.Strings(compiled.Required)
		}
	} else if len(spec.Properties) > 0 || spec.Required != nil || spec.AdditionalProperties != nil || spec.AdditionalPropertySchema != nil {
		return nil, fmt.Errorf("%s: object constraints require type object", path)
	}
	if spec.Items != nil {
		if spec.Type != "array" {
			return nil, fmt.Errorf("%s: items requires type array", path)
		}
		items, err := resolver.compileSchema(spec.Items, path+".items", mode, false, depth+1)
		if err != nil {
			return nil, err
		}
		compiled.Items = items
	}
	if spec.Type == "array" && spec.Items == nil {
		return nil, fmt.Errorf("%s: array items schema is required", path)
	}
	if spec.AdditionalPropertySchema != nil {
		if spec.Type != "object" || spec.AdditionalProperties != nil {
			return nil, fmt.Errorf("%s: additional_property_schema requires an object without additional_properties", path)
		}
		additional, err := resolver.compileSchema(spec.AdditionalPropertySchema, path+".additional_property_schema", mode, false, depth+1)
		if err != nil {
			return nil, err
		}
		compiled.AdditionalPropertySchema = additional
	}
	if spec.Required != nil && spec.Type != "object" {
		return nil, fmt.Errorf("%s: required is only valid for objects", path)
	}
	if spec.Enum != nil {
		if len(*spec.Enum) == 0 {
			return nil, fmt.Errorf("%s: enum cannot be empty", path)
		}
		if spec.Type == "" || spec.Type == "object" || spec.Type == "array" {
			return nil, fmt.Errorf("%s: enum requires a scalar type", path)
		}
		compiled.Enum = append([]any(nil), (*spec.Enum)...)
		for index, candidate := range compiled.Enum {
			if !compiledTypeMatches(compiled.Type, candidate) {
				return nil, fmt.Errorf("%s.enum[%d]: value has wrong type", path, index)
			}
			for previous := 0; previous < index; previous++ {
				if schemaEqual(candidate, compiled.Enum[previous]) {
					return nil, fmt.Errorf("%s.enum[%d]: duplicate value", path, index)
				}
			}
		}
	}
	if spec.Const.Kind != 0 {
		value, err := yamlValue(&spec.Const)
		if err != nil {
			return nil, fmt.Errorf("%s.const: %w", path, err)
		}
		compiled.Const, compiled.HasConst = value, true
		if compiled.Type != "" && !compiledTypeMatches(compiled.Type, value) {
			return nil, fmt.Errorf("%s.const: value has wrong type", path)
		}
	}
	if spec.AllOf != nil {
		if len(*spec.AllOf) == 0 {
			return nil, fmt.Errorf("%s: all_of cannot be empty", path)
		}
		for index := range *spec.AllOf {
			child, err := resolver.compileSchema(&(*spec.AllOf)[index], fmt.Sprintf("%s.all_of[%d]", path, index), mode, false, depth+1)
			if err != nil {
				return nil, err
			}
			compiled.AllOf = append(compiled.AllOf, child)
		}
	}
	if spec.OneOf != nil {
		if len(*spec.OneOf) == 0 {
			return nil, fmt.Errorf("%s: one_of cannot be empty", path)
		}
		for index := range *spec.OneOf {
			child, err := resolver.compileSchema(&(*spec.OneOf)[index], fmt.Sprintf("%s.one_of[%d]", path, index), mode, false, depth+1)
			if err != nil {
				return nil, err
			}
			compiled.OneOf = append(compiled.OneOf, child)
		}
	}
	if spec.AnyOf != nil {
		if len(*spec.AnyOf) == 0 {
			return nil, fmt.Errorf("%s: any_of cannot be empty", path)
		}
		for index := range *spec.AnyOf {
			child, err := resolver.compileSchema(&(*spec.AnyOf)[index], fmt.Sprintf("%s.any_of[%d]", path, index), mode, false, depth+1)
			if err != nil {
				return nil, err
			}
			compiled.AnyOf = append(compiled.AnyOf, child)
		}
	}
	if spec.Not != nil {
		notSchema, err := resolver.compileSchema(spec.Not, path+".not", mode, false, depth+1)
		if err != nil {
			return nil, err
		}
		compiled.Not = notSchema
	}
	if spec.If != nil {
		ifSchema, err := resolver.compileSchema(spec.If, path+".if", mode, false, depth+1)
		if err != nil {
			return nil, err
		}
		compiled.If = ifSchema
	}
	if spec.Then != nil {
		thenSchema, err := resolver.compileSchema(spec.Then, path+".then", mode, false, depth+1)
		if err != nil {
			return nil, err
		}
		compiled.Then = thenSchema
	}
	if compiled.If != nil && compiled.Then == nil || compiled.If == nil && compiled.Then != nil {
		return nil, fmt.Errorf("%s: if and then must be declared together", path)
	}
	if err := validateConstraintKinds(compiled, path); err != nil {
		return nil, err
	}
	if err := validateSchemaLiterals(compiled, path); err != nil {
		return nil, err
	}
	if spec.Default.Kind != 0 {
		if mode == schemaModeOutput {
			return nil, fmt.Errorf("%s: defaults are not allowed in output contracts", path)
		}
		value, err := yamlValue(&spec.Default)
		if err != nil {
			return nil, fmt.Errorf("%s.default: %w", path, err)
		}
		compiled.Default, compiled.HasDefault = value, true
		if err := validateCompiledSchema(compiled, value, path+".default"); err != nil {
			return nil, fmt.Errorf("%s.default: %w", path, err)
		}
	}
	if spec.DefaultOverride != nil {
		if mode == schemaModeOutput {
			return nil, fmt.Errorf("%s: defaults are not allowed in output contracts", path)
		}
		if err := applyDefaultOverride(compiled, spec.DefaultOverride, path); err != nil {
			return nil, err
		}
	}
	return compiled, nil
}

func mergeRefConstraints(schema *CompiledSchema, spec *schemaSpec, path string) error {
	if err := mergeLowerInt(&schema.MinLength, spec.MinLength, path+".min_length"); err != nil {
		return err
	}
	if err := mergeUpperInt(&schema.MaxLength, spec.MaxLength, path+".max_length"); err != nil {
		return err
	}
	if err := mergeLowerFloat(&schema.Minimum, spec.Minimum, path+".minimum"); err != nil {
		return err
	}
	if err := mergeUpperFloat(&schema.Maximum, spec.Maximum, path+".maximum"); err != nil {
		return err
	}
	if err := mergeLowerInt(&schema.MinItems, spec.MinItems, path+".min_items"); err != nil {
		return err
	}
	if err := mergeUpperInt(&schema.MaxItems, spec.MaxItems, path+".max_items"); err != nil {
		return err
	}
	if spec.UniqueItems != nil {
		if schema.UniqueItems != nil && *schema.UniqueItems && !*spec.UniqueItems {
			return fmt.Errorf("%s.unique_items: a ref cannot loosen the shared constraint", path)
		}
		if schema.UniqueItems == nil || *spec.UniqueItems {
			schema.UniqueItems = cloneBool(spec.UniqueItems)
		}
	}
	return nil
}

func mergeLowerInt(current **int, candidate *int, path string) error {
	if candidate == nil {
		return nil
	}
	if *current != nil && *candidate < **current {
		return fmt.Errorf("%s: a ref cannot loosen the shared constraint", path)
	}
	*current = cloneInt(candidate)
	return nil
}

func mergeUpperInt(current **int, candidate *int, path string) error {
	if candidate == nil {
		return nil
	}
	if *current != nil && *candidate > **current {
		return fmt.Errorf("%s: a ref cannot loosen the shared constraint", path)
	}
	*current = cloneInt(candidate)
	return nil
}

func mergeLowerFloat(current **float64, candidate *float64, path string) error {
	if candidate == nil {
		return nil
	}
	if *current != nil && *candidate < **current {
		return fmt.Errorf("%s: a ref cannot loosen the shared constraint", path)
	}
	*current = cloneFloat(candidate)
	return nil
}

func mergeUpperFloat(current **float64, candidate *float64, path string) error {
	if candidate == nil {
		return nil
	}
	if *current != nil && *candidate > **current {
		return fmt.Errorf("%s: a ref cannot loosen the shared constraint", path)
	}
	*current = cloneFloat(candidate)
	return nil
}

func containsSchemaValue(values []any, target any) bool {
	for _, value := range values {
		if schemaEqual(value, target) {
			return true
		}
	}
	return false
}

func applyDefaultOverride(schema *CompiledSchema, override *defaultOverrideSpec, path string) error {
	if override.Value.Kind == 0 {
		return fmt.Errorf("%s.default_override: value is required", path)
	}
	reason := strings.TrimSpace(override.Reason)
	if reason == "" || utf8.RuneCountInString(reason) > 256 || strings.ContainsAny(reason, "\x00\r\n") {
		return fmt.Errorf("%s.default_override: reason must be one bounded non-empty line", path)
	}
	if !schema.HasDefault {
		return fmt.Errorf("%s.default_override: no base default exists to override", path)
	}
	value, err := yamlValue(&override.Value)
	if err != nil {
		return fmt.Errorf("%s.default_override.value: %w", path, err)
	}
	if err := validateCompiledSchema(schema, value, path+".default_override.value"); err != nil {
		return fmt.Errorf("%s.default_override.value: %w", path, err)
	}
	schema.Default = value
	schema.DefaultOverrideReason = reason
	return nil
}

func validateSchemaLiterals(schema *CompiledSchema, path string) error {
	for index, value := range schema.Enum {
		if err := validateCompiledSchema(schema, value, fmt.Sprintf("%s.enum[%d]", path, index)); err != nil {
			return fmt.Errorf("%s.enum[%d]: %w", path, index, err)
		}
	}
	if schema.HasConst {
		if err := validateCompiledSchema(schema, schema.Const, path+".const"); err != nil {
			return fmt.Errorf("%s.const: %w", path, err)
		}
	}
	return nil
}

func validateConstraintKinds(schema *CompiledSchema, path string) error {
	if schema.Pattern != "" {
		if len(schema.Pattern) > 256 {
			return fmt.Errorf("%s: pattern exceeds maximum length", path)
		}
		if schema.Type != "string" {
			return fmt.Errorf("%s: pattern requires type string", path)
		}
		if _, err := regexp.Compile(schema.Pattern); err != nil {
			return fmt.Errorf("%s: invalid pattern: %w", path, err)
		}
	}
	if schema.MinLength != nil || schema.MaxLength != nil || schema.Format != "" {
		if schema.Type != "string" {
			return fmt.Errorf("%s: string constraints require type string", path)
		}
	}
	if schema.MinLength != nil && *schema.MinLength < 0 || schema.MaxLength != nil && *schema.MaxLength < 0 {
		return fmt.Errorf("%s: string bounds cannot be negative", path)
	}
	if schema.MinLength != nil && schema.MaxLength != nil && *schema.MinLength > *schema.MaxLength {
		return fmt.Errorf("%s: min_length exceeds max_length", path)
	}
	if schema.Minimum != nil || schema.Maximum != nil {
		if schema.Type != "integer" && schema.Type != "number" {
			return fmt.Errorf("%s: numeric bounds require integer or number type", path)
		}
		if schema.Minimum != nil && (math.IsNaN(*schema.Minimum) || math.IsInf(*schema.Minimum, 0)) || schema.Maximum != nil && (math.IsNaN(*schema.Maximum) || math.IsInf(*schema.Maximum, 0)) {
			return fmt.Errorf("%s: numeric bounds must be finite", path)
		}
		if schema.Type == "integer" && (schema.Minimum != nil && math.Trunc(*schema.Minimum) != *schema.Minimum || schema.Maximum != nil && math.Trunc(*schema.Maximum) != *schema.Maximum) {
			return fmt.Errorf("%s: integer bounds must be whole numbers", path)
		}
		if schema.Minimum != nil && schema.Maximum != nil && *schema.Minimum > *schema.Maximum {
			return fmt.Errorf("%s: minimum exceeds maximum", path)
		}
	}
	if schema.MinItems != nil || schema.MaxItems != nil || schema.UniqueItems != nil {
		if schema.Type != "array" {
			return fmt.Errorf("%s: array bounds require type array", path)
		}
	}
	if schema.MinItems != nil && *schema.MinItems < 0 || schema.MaxItems != nil && *schema.MaxItems < 0 {
		return fmt.Errorf("%s: array bounds cannot be negative", path)
	}
	if schema.MinItems != nil && schema.MaxItems != nil && *schema.MinItems > *schema.MaxItems {
		return fmt.Errorf("%s: min_items exceeds max_items", path)
	}
	return nil
}

func compileMetadata(path string, spec metadataSpec) (ActionMetadata, error) {
	if spec.Surface != surfaceNormal && spec.Surface != surfaceDebug {
		return ActionMetadata{}, fmt.Errorf("action %s: metadata.surface must be normal or debug", path)
	}
	if strings.HasPrefix(path, "debug/") != (spec.Surface == surfaceDebug) {
		return ActionMetadata{}, fmt.Errorf("action %s: debug surface must match debug domain", path)
	}
	if spec.Selector == nil || *spec.Selector != "key" && *spec.Selector != "none" {
		return ActionMetadata{}, fmt.Errorf("action %s: metadata.selector must be key or none", path)
	}
	if spec.Annotations == nil || spec.Annotations.ReadOnly == nil || spec.Annotations.Destructive == nil || spec.Annotations.Idempotent == nil || spec.Annotations.OpenWorld == nil {
		return ActionMetadata{}, fmt.Errorf("action %s: all annotation metadata fields are required", path)
	}
	hints := AnnotationHints{
		ReadOnly:    *spec.Annotations.ReadOnly,
		Destructive: *spec.Annotations.Destructive,
		Idempotent:  *spec.Annotations.Idempotent,
		OpenWorld:   *spec.Annotations.OpenWorld,
	}
	if hints.ReadOnly && hints.Destructive {
		return ActionMetadata{}, fmt.Errorf("action %s: a destructive action cannot be read-only", path)
	}
	return ActionMetadata{
		Surface:     spec.Surface,
		Selector:    *spec.Selector,
		Annotations: hints,
	}, nil
}

func validateDefinitionMetadata(name string, metadata definitionMetadata) error {
	expectedKinds := map[string]string{
		"WorkflowRole": "enum", "EntityKeyAndReference": "entity-reference", "Track": "entity", "Milestone": "entity",
		"Revision": "scalar", "CursorAndCompactHandle": "cursor", "GitFingerprint": "fingerprint",
		"NonGitDigestOrHandle": "compact-reference", "MutationReason": "scalar", "Timestamp": "timestamp",
		"Duration": "duration", "TrackReviewSnapshot": "public-review-snapshot", "TaskExecution": "public-task-execution",
		"TaskVerification": "task-verification", "Operation": "durable-operation",
	}
	if expected, ok := expectedKinds[name]; ok && metadata.Kind != expected {
		return fmt.Errorf("shared definitions.%s.metadata.kind must be %q", name, expected)
	}
	allowed := map[string]bool{"kind": true}
	switch name {
	case "EntityKeyAndReference":
		allowed["selector_field"], allowed["cross_domain_fields"] = true, true
	case "CursorAndCompactHandle":
		allowed["continuation_output"] = true
	case "GitFingerprint", "NonGitDigestOrHandle":
		allowed["authority"] = true
	case "Timestamp":
		allowed["timezone"], allowed["precision"], allowed["year_mapping"] = true, true, true
	case "Duration":
		allowed["unit"] = true
	case "Operation":
		allowed["kind_values_source"] = true
	}
	values := map[string]string{
		"selector_field": metadata.SelectorField, "continuation_output": metadata.ContinuationOutput,
		"authority": metadata.Authority, "timezone": metadata.Timezone, "precision": metadata.Precision,
		"year_mapping": metadata.YearMapping, "unit": metadata.Unit, "kind_values_source": metadata.KindValuesSource,
	}
	valueKeys := make([]string, 0, len(values))
	for key := range values {
		valueKeys = append(valueKeys, key)
	}
	sort.Strings(valueKeys)
	for _, key := range valueKeys {
		if values[key] != "" && !allowed[key] {
			return fmt.Errorf("shared definitions.%s.metadata.%s is not valid for this definition", name, key)
		}
	}
	if len(metadata.CrossDomainFields) > 0 && !allowed["cross_domain_fields"] {
		return fmt.Errorf("shared definitions.%s.metadata.cross_domain_fields is not valid for this definition", name)
	}
	switch name {
	case "EntityKeyAndReference":
		if metadata.SelectorField != "key" || len(metadata.CrossDomainFields) == 0 {
			return fmt.Errorf("shared definitions.%s.metadata must define key and semantic cross-domain fields", name)
		}
		seen := make(map[string]struct{}, len(metadata.CrossDomainFields))
		for _, field := range metadata.CrossDomainFields {
			if !validPropertyName(field) || field == "key" {
				return fmt.Errorf("shared definitions.%s.metadata has invalid cross-domain field %q", name, field)
			}
			if _, exists := seen[field]; exists {
				return fmt.Errorf("shared definitions.%s.metadata duplicates cross-domain field %q", name, field)
			}
			seen[field] = struct{}{}
		}
	case "CursorAndCompactHandle":
		if metadata.ContinuationOutput != "call.pagination.next_cursor" {
			return fmt.Errorf("shared definitions.%s.metadata has invalid continuation output", name)
		}
	case "GitFingerprint":
		if metadata.Authority != "exact-full-git-object-id-internally" {
			return fmt.Errorf("shared definitions.GitFingerprint must preserve full internal Git authority")
		}
	case "NonGitDigestOrHandle":
		if metadata.Authority != "server-owned-handle-resolves-to-exact-full-state" {
			return fmt.Errorf("shared definitions.%s.metadata must preserve server-owned exact authority", name)
		}
	case "Timestamp":
		if metadata.Timezone != "UTC-implicit" || metadata.Precision != "whole-seconds" || metadata.YearMapping != "2000-2099" {
			return fmt.Errorf("shared definitions.Timestamp metadata does not match ADR134")
		}
	case "Duration":
		if metadata.Unit != "per-action-seconds-or-minutes" {
			return fmt.Errorf("shared definitions.Duration unit must preserve explicit per-action seconds or minutes")
		}
	case "Operation":
		if metadata.KindValuesSource != "current-local-operation-producers" {
			return fmt.Errorf("shared definitions.Operation must identify the current kind source")
		}
	}
	return nil
}

func validateTimestampDefinition(definitions map[string]*CompiledSchema, metadata map[string]definitionMetadata) error {
	count := 0
	names := make([]string, 0, len(metadata))
	for name := range metadata {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if metadata[name].Kind == "timestamp" {
			count++
			if name != "Timestamp" {
				return fmt.Errorf("shared definitions: duplicate Timestamp scalar %q", name)
			}
		}
	}
	if count != 1 {
		return fmt.Errorf("shared definitions: exactly one Timestamp scalar is required")
	}
	timestamp := definitions["Timestamp"]
	if timestamp == nil || timestamp.Type != "string" || timestamp.Format != formatTimestamp || timestamp.MinLength == nil || timestamp.MaxLength == nil || *timestamp.MinLength != 17 || *timestamp.MaxLength != 17 || timestamp.Pattern == "" || len(timestamp.Enum) != 0 || timestamp.HasDefault || timestamp.HasConst {
		return fmt.Errorf("shared definitions.Timestamp schema must use the compact ADR134 scalar")
	}
	pattern, err := regexp.Compile(timestamp.Pattern)
	if err != nil {
		return fmt.Errorf("shared definitions.Timestamp pattern is invalid: %w", err)
	}
	for _, value := range []string{"00-01-01T00:00:00", "99-12-31T23:59:59"} {
		if !pattern.MatchString(value) {
			return fmt.Errorf("shared definitions.Timestamp pattern rejects valid value %q", value)
		}
	}
	for _, value := range []string{"2026-09-23T13:42:12Z", "x00-01-01T00:00:00", "00-01-01T00:00:00x", "00-01-01t00:00:00", "00-13-01T00:00:00", "00-01-01T24:00:00", "00-01-01T00:00:60"} {
		if pattern.MatchString(value) {
			return fmt.Errorf("shared definitions.Timestamp pattern accepts invalid value %q", value)
		}
	}
	return nil
}

func validType(value string) bool {
	switch value {
	case "array", "boolean", "integer", "null", "number", "object", "string":
		return true
	default:
		return false
	}
}

func validDefinitionName(value string) bool {
	if value == "" || value[0] < 'A' || value[0] > 'Z' {
		return false
	}
	for _, char := range value[1:] {
		if !(char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}

func validName(value string) bool {
	return namePattern.MatchString(value)
}

func validDurationUnit(value string) bool {
	return value == "seconds" || value == "minutes"
}

func validPropertyName(value string) bool {
	return namePattern.MatchString(value)
}

func validPathPart(value string) bool {
	return actionPattern.MatchString(value)
}

func splitActionPath(path string) (string, string, bool) {
	parts := strings.Split(path, "/")
	if len(parts) != 2 || !validName(parts[0]) || !validPathPart(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func isRetiredDomain(domain string) bool {
	switch domain {
	case "queue", "queues", "train", "trains", "hotfix", "hotfixes", "attempt", "attempts", "wave", "waves", "deploy", "deployment", "deployments":
		return true
	default:
		return false
	}
}

func isRetiredAction(path string) bool {
	if path == "task/submit-tests" || path == "system/call" || path == "system/schema" || path == "git/refs" || path == "git/log" || path == "git/tree" || path == "debug/task_legacy_revision_list" || path == "debug/task_legacy_revision_read" {
		return true
	}
	_, name, _ := splitActionPath(path)
	retiredFamilies := []string{"queue", "queues", "train", "trains", "hotfix", "hotfixes", "attempt", "attempts", "wave", "waves", "deploy", "deployment", "deployments"}
	for _, part := range strings.FieldsFunc(name, func(char rune) bool { return char == '_' || char == '-' }) {
		if containsString(retiredFamilies, part) {
			return true
		}
	}
	return false
}

func rootSchemaCanBeObject(schema *CompiledSchema) bool {
	if schema == nil || schema.Type != "" && schema.Type != "object" {
		return false
	}
	canBeObject := schema.Type == "object"
	for _, candidate := range schema.AllOf {
		if !rootSchemaCanBeObject(candidate) {
			return false
		}
		canBeObject = true
	}
	for _, alternatives := range [][]*CompiledSchema{schema.OneOf, schema.AnyOf} {
		if len(alternatives) == 0 {
			continue
		}
		matched := false
		for _, candidate := range alternatives {
			if rootSchemaCanBeObject(candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
		canBeObject = true
	}
	return canBeObject
}

func hasRefStructuralFields(spec *schemaSpec) bool {
	return spec.Type != "" || len(spec.Properties) > 0 || spec.Required != nil || spec.Items != nil || spec.AdditionalProperties != nil || spec.AdditionalPropertySchema != nil || spec.Format != "" || spec.AllOf != nil || spec.OneOf != nil || spec.AnyOf != nil || spec.Not != nil || spec.If != nil || spec.Then != nil
}

func validateDurationUnits(schema *CompiledSchema, path string) error {
	if schema == nil {
		return nil
	}
	if schema.RefName == "Duration" && !validDurationUnit(schema.Unit) {
		return fmt.Errorf("%s: Duration reference requires an action-specific unit", path)
	}
	for _, key := range sortedCompiledKeys(schema.Properties) {
		child := schema.Properties[key]
		if err := validateDurationUnits(child, path+".properties."+key); err != nil {
			return err
		}
	}
	if err := validateDurationUnits(schema.Items, path+".items"); err != nil {
		return err
	}
	if err := validateDurationUnits(schema.AdditionalPropertySchema, path+".additional_property_schema"); err != nil {
		return err
	}
	for _, child := range append(append(append([]*CompiledSchema{}, schema.AllOf...), schema.OneOf...), schema.AnyOf...) {
		if err := validateDurationUnits(child, path); err != nil {
			return err
		}
	}
	return nil
}

func validatePublicContract(path, surface string, input, output *CompiledSchema, crossDomainFields []string, openWorld bool) error {
	if err := validateActionSchemaFields(path, "input", input, surface, crossDomainFields, nil); err != nil {
		return err
	}
	if err := validateActionSchemaFields(path, "output", output, surface, crossDomainFields, map[string]bool{}); err != nil {
		return err
	}
	if schemaAllowsUnknownProperties(output) && !openWorld {
		return fmt.Errorf("action %s: open additional properties require open_world metadata", path)
	}
	return nil
}

func validateActionSchemaFields(actionPath, direction string, schema *CompiledSchema, surface string, crossDomainFields []string, seen map[string]bool) error {
	if schema == nil {
		return nil
	}
	if seen != nil {
		if seen[schema.RefName] && schema.RefName != "" {
			return nil
		}
		if schema.RefName != "" {
			seen[schema.RefName] = true
		}
	}
	if schema.RefName != "Timestamp" && hasTimestampPattern(schema.Pattern) {
		return fmt.Errorf("action %s.%s: Timestamp patterns must use the shared Timestamp definition", actionPath, direction)
	}
	for _, key := range sortedCompiledKeys(schema.Properties) {
		child := schema.Properties[key]
		if key == "runtime_ref" || strings.HasSuffix(key, "_runtime_ref") {
			return fmt.Errorf("action %s.%s: runtime-ref fields are not contract-authorized", actionPath, direction)
		}
		if key == "agent_ref" && surface != surfaceDebug {
			return fmt.Errorf("action %s.%s: agent_ref is only allowed on the debug surface", actionPath, direction)
		}
		if key == "role_gate" || key == "required_role" || key == "allowed_roles" || key == "authority_role" {
			return fmt.Errorf("action %s.%s: distributed role-gate fields are not contract-authorized", actionPath, direction)
		}
		if key == "id" || strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "_key") {
			return fmt.Errorf("action %s.%s: identity alias field %q is not allowed", actionPath, direction, key)
		}
		if isTimestampField(key) && child.RefName != "Timestamp" {
			return fmt.Errorf("action %s.%s: timestamp field %q must reference Timestamp", actionPath, direction, key)
		}
		if isGitFingerprintField(key) && child.RefName != "GitFingerprint" {
			return fmt.Errorf("action %s.%s: Git fingerprint field %q must reference GitFingerprint", actionPath, direction, key)
		}
		if (key == "duration" || strings.HasSuffix(key, "_duration")) && child.RefName != "Duration" {
			return fmt.Errorf("action %s.%s: duration field %q must reference Duration", actionPath, direction, key)
		}
		if isDigestField(key) && child.RefName != "NonGitDigestOrHandle" {
			return fmt.Errorf("action %s.%s: non-Git digest field %q must be omitted or use NonGitDigestOrHandle", actionPath, direction, key)
		}
		if direction == "input" && (key == "reason" || strings.HasSuffix(key, "_reason")) && child.RefName != "MutationReason" {
			return fmt.Errorf("action %s.input: mutation reason field %q must reference MutationReason", actionPath, key)
		}
		if key == "next_cursor" || key == "has_more" || key == "_pagination" || key == "pagination" || direction == "output" && key == "cursor" {
			return fmt.Errorf("action %s.%s: continuation belongs to the outer pagination contract", actionPath, direction)
		}
		if direction == "input" && key == "cursor" && child.RefName != "CursorAndCompactHandle" {
			return fmt.Errorf("action %s.input: cursor must reference CursorAndCompactHandle", actionPath)
		}
		if isKnownCrossDomainField(crossDomainFields, key) && !isEntityReference(child) {
			return fmt.Errorf("action %s.%s: cross-domain field %q must use a semantic EntityKeyAndReference", actionPath, direction, key)
		}
		if err := validateActionSchemaFields(actionPath, direction, child, surface, crossDomainFields, seen); err != nil {
			return err
		}
	}
	if schema.Items != nil {
		if err := validateActionSchemaFields(actionPath, direction, schema.Items, surface, crossDomainFields, seen); err != nil {
			return err
		}
	}
	if schema.AdditionalPropertySchema != nil {
		if err := validateActionSchemaFields(actionPath, direction, schema.AdditionalPropertySchema, surface, crossDomainFields, seen); err != nil {
			return err
		}
	}
	for _, child := range append(append(append([]*CompiledSchema{}, schema.AllOf...), schema.OneOf...), schema.AnyOf...) {
		if err := validateActionSchemaFields(actionPath, direction, child, surface, crossDomainFields, seen); err != nil {
			return err
		}
	}
	if schema.Not != nil {
		if err := validateActionSchemaFields(actionPath, direction, schema.Not, surface, crossDomainFields, seen); err != nil {
			return err
		}
	}
	if schema.If != nil {
		if err := validateActionSchemaFields(actionPath, direction, schema.If, surface, crossDomainFields, seen); err != nil {
			return err
		}
	}
	if schema.Then != nil {
		return validateActionSchemaFields(actionPath, direction, schema.Then, surface, crossDomainFields, seen)
	}
	return nil
}

func isEntityReference(schema *CompiledSchema) bool {
	if schema == nil {
		return false
	}
	if schema.RefName == "EntityKeyAndReference" {
		return true
	}
	key := schema.Properties["key"]
	return schema.Type == "object" && key != nil && key.RefName == "EntityKeyAndReference"
}

func hasTimestampPattern(pattern string) bool {
	return strings.Contains(pattern, "T") && strings.Contains(pattern, ":")
}

func isTimestampField(name string) bool {
	return name == "time" || name == "timestamp" || strings.HasSuffix(name, "_at") || strings.HasSuffix(name, "_time") || strings.HasSuffix(name, "_timestamp")
}

func isGitFingerprintField(name string) bool {
	return name == "head" || name == "tree" || name == "base" || name == "commit" || name == "fingerprint" || name == "git_hash" || name == "hub_revision" || strings.HasSuffix(name, "_head") || strings.HasSuffix(name, "_tree") || strings.HasSuffix(name, "_commit") || strings.HasSuffix(name, "_fingerprint") || strings.HasSuffix(name, "_base") || strings.HasSuffix(name, "_sha")
}

func isDigestField(name string) bool {
	return !isGitFingerprintField(name) && (name == "digest" || name == "hash" || strings.HasSuffix(name, "_sha256") || strings.HasSuffix(name, "_digest") || strings.HasSuffix(name, "_hash"))
}

func schemaAllowsUnknownProperties(schema *CompiledSchema) bool {
	if schema == nil {
		return false
	}
	if schema.AdditionalProperties != nil && *schema.AdditionalProperties {
		return true
	}
	if schema.AdditionalPropertySchema != nil {
		return true
	}
	if schema.Items != nil && schemaAllowsUnknownProperties(schema.Items) {
		return true
	}
	for _, child := range schema.Properties {
		if schemaAllowsUnknownProperties(child) {
			return true
		}
	}
	for _, child := range append(append(append([]*CompiledSchema{}, schema.AllOf...), schema.OneOf...), schema.AnyOf...) {
		if schemaAllowsUnknownProperties(child) {
			return true
		}
	}
	return schema.Not != nil && schemaAllowsUnknownProperties(schema.Not) || schema.If != nil && schemaAllowsUnknownProperties(schema.If) || schema.Then != nil && schemaAllowsUnknownProperties(schema.Then)
}

func sortedSchemaKeys(values map[string]schemaSpec) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedDefinitionNames(values map[string]definitionSpec) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type schemaMode int

const (
	schemaModeShared schemaMode = iota
	schemaModeInput
	schemaModeOutput
)
