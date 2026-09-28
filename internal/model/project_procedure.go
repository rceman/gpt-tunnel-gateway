package model

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxProjectProcedures = 32

const (
	HookPreTaskSubmit         = "pre_task_submit"
	HookPostTaskSubmit        = "post_task_submit"
	HookPreTaskVerify         = "pre_task_verify"
	HookPostTaskVerify        = "post_task_verify"
	HookPreTaskIntegrate      = "pre_task_integrate"
	HookPostTaskIntegrate     = "post_task_integrate"
	HookPostAgentWorkFinished = "post_agent_work_finished"
)

var procedureNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var procedurePropertyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

var projectHookNames = []string{
	HookPreTaskSubmit,
	HookPostTaskSubmit,
	HookPreTaskVerify,
	HookPostTaskVerify,
	HookPreTaskIntegrate,
	HookPostTaskIntegrate,
	HookPostAgentWorkFinished,
}

var procedureSharedReferences = map[string]struct{}{
	"EntityKeyAndReference": {},
	"Revision":              {},
	"GitFingerprint":        {},
	"MutationReason":        {},
	"Timestamp":             {},
}

var procedureEntityFields = map[string]struct{}{
	"project":   {},
	"task":      {},
	"track":     {},
	"operation": {},
	"session":   {},
	"agent":     {},
}

type ProjectProcedureDefinition struct {
	Script  string         `json:"script"`
	Summary string         `json:"summary"`
	Guide   string         `json:"guide"`
	Input   map[string]any `json:"input"`
	Output  map[string]any `json:"output"`
}

func ProjectHookNames() []string {
	return append([]string(nil), projectHookNames...)
}

func IsProjectHookName(name string) bool {
	for _, candidate := range projectHookNames {
		if name == candidate {
			return true
		}
	}
	return false
}

func ValidateProcedureName(name string) error {
	if !procedureNamePattern.MatchString(name) {
		return fmt.Errorf("invalid ProcedureName")
	}
	return nil
}

func ValidateProjectProcedureDefinition(value ProjectProcedureDefinition) error {
	if !validProcedureScriptPath(value.Script) {
		return fmt.Errorf("invalid Procedure script path")
	}
	if !validProcedureText(value.Summary, 1, 256, true) || !validProcedureText(value.Guide, 1, 768, false) {
		return fmt.Errorf("invalid Procedure summary or guide")
	}
	if err := ValidateProcedureSchema(value.Input); err != nil {
		return fmt.Errorf("Procedure input schema: %w", err)
	}
	if err := ValidateProcedureSchema(value.Output); err != nil {
		return fmt.Errorf("Procedure output schema: %w", err)
	}
	return nil
}

func ValidateProcedureSchema(schema map[string]any) error {
	if schema == nil {
		return fmt.Errorf("schema is required")
	}
	encoded, err := json.Marshal(schema)
	if err != nil || len(encoded) > 16<<10 {
		return fmt.Errorf("schema exceeds encoded size bound")
	}
	if typ, _ := schema["type"].(string); typ != "object" {
		return fmt.Errorf("schema root must be an object")
	}
	state := procedureSchemaValidation{nodes: 0}
	if err := state.validate(schema, 1, false); err != nil {
		return err
	}
	return nil
}

func ValidateProjectProcedures(procedures map[string]ProjectProcedureDefinition) error {
	if procedures == nil || len(procedures) > MaxProjectProcedures {
		return fmt.Errorf("Procedure catalogue must contain 0..%d entries", MaxProjectProcedures)
	}
	for _, name := range sortedProcedureNames(procedures) {
		if err := ValidateProcedureName(name); err != nil {
			return fmt.Errorf("Procedure %q: %w", name, err)
		}
		if err := ValidateProjectProcedureDefinition(procedures[name]); err != nil {
			return fmt.Errorf("Procedure %q: %w", name, err)
		}
	}
	return nil
}

func ValidateProjectHooks(hooks map[string]string, procedures map[string]ProjectProcedureDefinition) error {
	if hooks == nil || len(hooks) > len(projectHookNames) {
		return fmt.Errorf("Hook bindings must contain 0..%d entries", len(projectHookNames))
	}
	for hook, procedure := range hooks {
		if !IsProjectHookName(hook) {
			return fmt.Errorf("unknown server-owned Hook %q", hook)
		}
		if err := ValidateProcedureName(procedure); err != nil {
			return fmt.Errorf("Hook %q has invalid Procedure reference", hook)
		}
		definition, ok := procedures[procedure]
		if !ok {
			return fmt.Errorf("Hook %q references missing Procedure %q", hook, procedure)
		}
		if err := ValidateProcedureHookCompatibility(hook, definition.Input); err != nil {
			return fmt.Errorf("Hook %q Procedure input: %w", hook, err)
		}
	}
	return nil
}

func ValidateProcedureHookCompatibility(hook string, procedureInput map[string]any) error {
	if hook == HookPostAgentWorkFinished {
		properties, _ := procedureInput["properties"].(map[string]any)
		required := map[string]struct{}{}
		if values, ok := procedureInput["required"].([]any); ok {
			for _, value := range values {
				if name, ok := value.(string); ok {
					required[name] = struct{}{}
				}
			}
		}
		for _, name := range []string{"epoch", "project"} {
			if _, present := properties[name]; !present {
				return fmt.Errorf("post_agent_work_finished requires Procedure property %q", name)
			}
			if _, present := required[name]; !present {
				return fmt.Errorf("post_agent_work_finished requires Procedure property %q", name)
			}
		}
	}
	payload, ok := ProjectHookPayloadSchema(hook)
	if !ok {
		return fmt.Errorf("unknown Hook")
	}
	properties, _ := procedureInput["properties"].(map[string]any)
	payloadProperties, _ := payload["properties"].(map[string]any)
	required := map[string]struct{}{}
	if values, ok := procedureInput["required"].([]any); ok {
		for _, value := range values {
			if name, ok := value.(string); ok {
				required[name] = struct{}{}
			}
		}
	}
	payloadRequired := map[string]struct{}{}
	if values, ok := payload["required"].([]any); ok {
		for _, value := range values {
			if name, ok := value.(string); ok {
				payloadRequired[name] = struct{}{}
			}
		}
	}
	for name, procedureSchemaValue := range properties {
		payloadSchemaValue, present := payloadProperties[name]
		if !present {
			if _, mustHave := required[name]; mustHave {
				return fmt.Errorf("required property %q is absent from canonical payload", name)
			}
			continue
		}
		if _, mustHave := required[name]; mustHave {
			if _, guaranteed := payloadRequired[name]; !guaranteed {
				return fmt.Errorf("required property %q is optional in canonical payload", name)
			}
		}
		procedureSchema, ok := procedureSchemaValue.(map[string]any)
		if !ok {
			return fmt.Errorf("Procedure property %q is invalid", name)
		}
		payloadSchema, ok := payloadSchemaValue.(map[string]any)
		if !ok || !procedureSchemaAcceptsPayload(procedureSchema, payloadSchema) {
			return fmt.Errorf("Procedure property %q is not compatible with canonical payload", name)
		}
	}
	return nil
}

func ProjectHookPayloadSchema(hook string) (map[string]any, bool) {
	entity := map[string]any{"$ref": "EntityKeyAndReference"}
	revision := map[string]any{"$ref": "Revision"}
	fingerprint := map[string]any{"$ref": "GitFingerprint"}
	properties := map[string]any{
		"project":   entity,
		"task":      entity,
		"session":   entity,
		"operation": entity,
	}
	required := []any{"project", "task", "session", "operation"}
	add := func(name string, schema map[string]any) {
		properties[name] = schema
		required = append(required, name)
	}
	switch hook {
	case HookPreTaskSubmit, HookPostTaskSubmit:
		stage := map[string]any{"type": "string", "enum": []any{"code", "rebase"}}
		add("stage", stage)
		add("task_revision", revision)
		add("execution_revision", revision)
		add("candidate_head", fingerprint)
		if hook == HookPostTaskSubmit {
			add("status", map[string]any{"type": "string", "enum": []any{"awaiting_review"}})
		}
	case HookPreTaskVerify, HookPostTaskVerify:
		delete(properties, "stage")
		add("task_revision", revision)
		add("execution_revision", revision)
		add("candidate_head", fingerprint)
		add("candidate_tree", fingerprint)
		add("main_base", fingerprint)
		if hook == HookPostTaskVerify {
			add("outcome", map[string]any{"type": "string", "enum": []any{"succeeded", "failed", "interrupted"}})
		}
	case HookPreTaskIntegrate, HookPostTaskIntegrate:
		add("task_revision", revision)
		add("execution_revision", revision)
		add("candidate_head", fingerprint)
		add("candidate_tree", fingerprint)
		add("main_base", fingerprint)
		if hook == HookPostTaskIntegrate {
			add("integration_head", fingerprint)
			add("status", map[string]any{"type": "string", "enum": []any{"integrated"}})
		}
	case HookPostAgentWorkFinished:
		properties = map[string]any{
			"epoch":   map[string]any{"type": "string", "minLength": 47, "maxLength": 47, "pattern": `^agent-work-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`},
			"project": entity,
			"agent":   entity,
		}
		required = []any{"epoch", "project"}
	default:
		return nil, false
	}
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}, true
}

func ProjectHookDescription(hook string) (string, bool) {
	descriptions := map[string]string{
		HookPreTaskSubmit:         "Runs after submission admission and before the Task becomes awaiting review.",
		HookPostTaskSubmit:        "Runs after the Task submission is durably committed.",
		HookPreTaskVerify:         "Runs after verification admission and before verification begins.",
		HookPostTaskVerify:        "Runs after verification outcome and execution state are durably committed.",
		HookPreTaskIntegrate:      "Runs after integration admission and before integration is committed.",
		HookPostTaskIntegrate:     "Runs after canonical integration is durably committed.",
		HookPostAgentWorkFinished: "Runs after a successfully dispatched Agent work epoch reaches stable idle.",
	}
	description, ok := descriptions[hook]
	return description, ok
}

func sortedProcedureNames(procedures map[string]ProjectProcedureDefinition) []string {
	names := make([]string, 0, len(procedures))
	for name := range procedures {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type procedureSchemaValidation struct {
	nodes int
}

func (state *procedureSchemaValidation) validate(schema map[string]any, depth int, inArrayItem bool) error {
	state.nodes++
	if depth > 8 || state.nodes > 256 {
		return fmt.Errorf("schema exceeds structural bounds")
	}
	if reference, ok := schema["$ref"]; ok {
		name, valid := reference.(string)
		if !valid || inArrayItem {
			return fmt.Errorf("invalid shared reference")
		}
		if _, valid := procedureSharedReferences[name]; !valid || len(schema) != 1 {
			return fmt.Errorf("unsupported shared reference")
		}
		return nil
	}
	for key := range schema {
		switch key {
		case "type", "properties", "required", "additionalProperties", "items", "minItems", "maxItems", "uniqueItems", "minLength", "maxLength", "enum", "minimum", "maximum":
		default:
			return fmt.Errorf("unsupported schema keyword %q", key)
		}
	}
	typ, ok := schema["type"].(string)
	if !ok {
		return fmt.Errorf("schema type is required")
	}
	var allowed []string
	switch typ {
	case "object":
		allowed = []string{"type", "properties", "required", "additionalProperties"}
	case "array":
		allowed = []string{"type", "items", "minItems", "maxItems", "uniqueItems"}
	case "string":
		allowed = []string{"type", "minLength", "maxLength", "enum"}
	case "integer", "number":
		allowed = []string{"type", "minimum", "maximum"}
	case "boolean":
		allowed = []string{"type"}
	default:
		return fmt.Errorf("unsupported schema type %q", typ)
	}
	if !procedureSchemaKeysAllowed(schema, allowed...) {
		return fmt.Errorf("schema contains a keyword incompatible with type %q", typ)
	}
	switch typ {
	case "object":
		if additional, exists := schema["additionalProperties"]; !exists || additional != false {
			return fmt.Errorf("object schemas must set additionalProperties:false")
		}
		properties, exists := schema["properties"].(map[string]any)
		if !exists || len(properties) > 32 {
			return fmt.Errorf("object properties exceed structural bounds")
		}
		required := map[string]struct{}{}
		if rawRequired, exists := schema["required"]; exists {
			values, ok := rawRequired.([]any)
			if !ok || len(values) > len(properties) {
				return fmt.Errorf("invalid required property list")
			}
			for _, value := range values {
				name, ok := value.(string)
				if !ok || !procedurePropertyPattern.MatchString(name) {
					return fmt.Errorf("invalid required property name")
				}
				if _, exists := properties[name]; !exists {
					return fmt.Errorf("required property %q is not declared", name)
				}
				if _, duplicate := required[name]; duplicate {
					return fmt.Errorf("duplicate required property %q", name)
				}
				required[name] = struct{}{}
			}
		}
		for name, rawProperty := range properties {
			if !procedurePropertyPattern.MatchString(name) || name == "id" || strings.HasSuffix(name, "_id") || strings.HasSuffix(name, "_key") {
				return fmt.Errorf("invalid or aliased property name %q", name)
			}
			property, ok := rawProperty.(map[string]any)
			if !ok {
				return fmt.Errorf("property %q must be a schema object", name)
			}
			if reference, ok := property["$ref"].(string); ok && reference == "EntityKeyAndReference" {
				if _, semantic := procedureEntityFields[name]; !semantic {
					return fmt.Errorf("EntityKeyAndReference is not allowed for property %q", name)
				}
			}
			if err := state.validate(property, depth+1, false); err != nil {
				return fmt.Errorf("property %q: %w", name, err)
			}
		}
	case "array":
		items, exists := schema["items"].(map[string]any)
		if !exists {
			return fmt.Errorf("array items schema is required")
		}
		if minimum, exists := schema["minItems"]; exists && !boundedSchemaInteger(minimum, 0, 64) {
			return fmt.Errorf("invalid minItems")
		}
		if maximum, exists := schema["maxItems"]; exists && !boundedSchemaInteger(maximum, 0, 64) {
			return fmt.Errorf("invalid maxItems")
		}
		if min, minOK := schemaInteger(schema["minItems"]); minOK {
			if max, maxOK := schemaInteger(schema["maxItems"]); maxOK && min > max {
				return fmt.Errorf("minItems exceeds maxItems")
			}
		}
		if unique, exists := schema["uniqueItems"]; exists && unique != true {
			return fmt.Errorf("uniqueItems may only be true")
		}
		if err := state.validate(items, depth+1, true); err != nil {
			return fmt.Errorf("array items: %w", err)
		}
	case "string":
		if minimum, exists := schema["minLength"]; exists && !boundedSchemaInteger(minimum, 0, 4096) {
			return fmt.Errorf("invalid minLength")
		}
		if maximum, exists := schema["maxLength"]; exists && !boundedSchemaInteger(maximum, 0, 4096) {
			return fmt.Errorf("invalid maxLength")
		}
		if min, minOK := schemaInteger(schema["minLength"]); minOK {
			if max, maxOK := schemaInteger(schema["maxLength"]); maxOK && min > max {
				return fmt.Errorf("minLength exceeds maxLength")
			}
		}
		if rawEnum, exists := schema["enum"]; exists {
			values, ok := rawEnum.([]any)
			if !ok || len(values) == 0 || len(values) > 64 {
				return fmt.Errorf("invalid string enum")
			}
			seen := map[string]struct{}{}
			for _, value := range values {
				text, ok := value.(string)
				if !ok || utf8.RuneCountInString(text) > 4096 {
					return fmt.Errorf("invalid string enum value")
				}
				if _, exists := seen[text]; exists {
					return fmt.Errorf("duplicate string enum value")
				}
				seen[text] = struct{}{}
			}
		}
	case "integer", "number":
		if _, exists := schema["minLength"]; exists || containsProcedureKeys(schema, "properties", "required", "items", "minItems", "maxItems", "uniqueItems", "enum") {
			return fmt.Errorf("invalid numeric schema")
		}
		minimum, minOK := schemaNumber(schema["minimum"])
		maximum, maxOK := schemaNumber(schema["maximum"])
		if _, exists := schema["minimum"]; exists && (!minOK || math.IsNaN(minimum) || math.IsInf(minimum, 0) || minimum < -9007199254740991 || minimum > 9007199254740991) {
			return fmt.Errorf("invalid minimum")
		}
		if _, exists := schema["maximum"]; exists && (!maxOK || math.IsNaN(maximum) || math.IsInf(maximum, 0) || maximum < -9007199254740991 || maximum > 9007199254740991) {
			return fmt.Errorf("invalid maximum")
		}
		if minOK && maxOK && minimum > maximum {
			return fmt.Errorf("minimum exceeds maximum")
		}
		if typ == "integer" && (minimum != math.Trunc(minimum) && minOK || maximum != math.Trunc(maximum) && maxOK) {
			return fmt.Errorf("integer bounds must be integers")
		}
	case "boolean":
		if len(schema) != 1 {
			return fmt.Errorf("invalid boolean schema")
		}
	default:
		return fmt.Errorf("unsupported schema type %q", typ)
	}
	return nil
}

func procedureSchemaAcceptsPayload(procedure, payload map[string]any) bool {
	if procedureRef, ok := procedure["$ref"]; ok {
		payloadRef, payloadOK := payload["$ref"]
		return payloadOK && procedureRef == payloadRef
	}
	procedureType, _ := procedure["type"].(string)
	payloadType, _ := payload["type"].(string)
	if procedureType != payloadType || procedureType == "" {
		return false
	}
	procedureEnum, procedureHasEnum := procedure["enum"].([]any)
	payloadEnum, payloadHasEnum := payload["enum"].([]any)
	if procedureHasEnum {
		if !payloadHasEnum {
			return false
		}
		for _, payloadValue := range payloadEnum {
			if !containsJSONValue(procedureEnum, payloadValue) {
				return false
			}
		}
	}
	for _, bound := range []string{"minLength", "minItems", "minimum"} {
		procedureValue, procedureHas := procedure[bound]
		if !procedureHas {
			continue
		}
		payloadValue, payloadHas := payload[bound]
		if !payloadHas {
			return false
		}
		payloadNumber, payloadOK := schemaNumber(payloadValue)
		procedureNumber, procedureOK := schemaNumber(procedureValue)
		if !payloadOK || !procedureOK || procedureNumber > payloadNumber {
			return false
		}
	}
	for _, bound := range []string{"maxLength", "maxItems", "maximum"} {
		procedureValue, procedureHas := procedure[bound]
		if !procedureHas {
			continue
		}
		payloadValue, payloadHas := payload[bound]
		if !payloadHas {
			return false
		}
		payloadNumber, payloadOK := schemaNumber(payloadValue)
		procedureNumber, procedureOK := schemaNumber(procedureValue)
		if !payloadOK || !procedureOK || procedureNumber < payloadNumber {
			return false
		}
	}
	if unique, procedureHas := procedure["uniqueItems"]; procedureHas && unique == true && payload["uniqueItems"] != true {
		return false
	}
	return true
}

func validProcedureScriptPath(path string) bool {
	if len(path) < 1 || len(path) > 512 || !utf8.ValidString(path) || strings.HasPrefix(path, "/") || strings.Contains(path, "\\") || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func validProcedureText(value string, minimum, maximum int, singleLine bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < minimum || utf8.RuneCountInString(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) && !(r == '\n' && !singleLine) {
			return false
		}
	}
	return true
}

func boundedSchemaInteger(value any, minimum, maximum int) bool {
	integer, ok := schemaInteger(value)
	return ok && integer >= minimum && integer <= maximum
}

func schemaInteger(value any) (int, bool) {
	number, ok := schemaNumber(value)
	if !ok || number != math.Trunc(number) || number < float64(math.MinInt) || number > float64(math.MaxInt) {
		return 0, false
	}
	return int(number), true
}

func schemaNumber(value any) (float64, bool) {
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
	case json.Number:
		value, err := number.Float64()
		return value, err == nil
	default:
		return 0, false
	}
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

func containsProcedureKeys(schema map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, exists := schema[key]; exists {
			return true
		}
	}
	return false
}

func containsJSONValue(values []any, want any) bool {
	for _, value := range values {
		left, leftErr := json.Marshal(value)
		right, rightErr := json.Marshal(want)
		if leftErr == nil && rightErr == nil && string(left) == string(right) {
			return true
		}
	}
	return false
}
