package actioncontract

import "gopkg.in/yaml.v3"

const catalogVersion = 1

const (
	formatTimestamp = "gtw-timestamp"
	surfaceNormal   = "normal"
	surfaceDebug    = "debug"
)

type sharedFile struct {
	Version     int                       `yaml:"version"`
	Definitions map[string]definitionSpec `yaml:"definitions"`
}

type definitionSpec struct {
	Schema   *schemaSpec         `yaml:"schema"`
	Metadata *definitionMetadata `yaml:"metadata"`
}

type definitionMetadata struct {
	Kind               string   `yaml:"kind"`
	SelectorField      string   `yaml:"selector_field,omitempty"`
	CrossDomainFields  []string `yaml:"cross_domain_fields,omitempty"`
	ContinuationOutput string   `yaml:"continuation_output,omitempty"`
	Authority          string   `yaml:"authority,omitempty"`
	Timezone           string   `yaml:"timezone,omitempty"`
	Precision          string   `yaml:"precision,omitempty"`
	YearMapping        string   `yaml:"year_mapping,omitempty"`
	Unit               string   `yaml:"unit,omitempty"`
	KindValuesSource   string   `yaml:"kind_values_source,omitempty"`
}

type catalogSpec struct {
	Version int          `yaml:"version"`
	Domains []domainSpec `yaml:"domains"`
}

type domainSpec struct {
	Name    string       `yaml:"name"`
	Actions []actionSpec `yaml:"actions"`
}

type actionSpec struct {
	Name        string        `yaml:"name"`
	Description string        `yaml:"description"`
	Metadata    *metadataSpec `yaml:"metadata"`
	Input       *schemaSpec   `yaml:"input"`
	Output      *schemaSpec   `yaml:"output"`
}

type metadataSpec struct {
	Surface       string             `yaml:"surface"`
	Selector      *string            `yaml:"selector"`
	Annotations   *annotationsSpec   `yaml:"annotations"`
	HandlerInput  []fieldMappingSpec `yaml:"handler_input,omitempty"`
	HandlerOutput []fieldMappingSpec `yaml:"handler_output,omitempty"`
}

type fieldMappingSpec struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

type FieldMapping struct {
	From string
	To   string
}

type annotationsSpec struct {
	ReadOnly    *bool `yaml:"read_only"`
	Destructive *bool `yaml:"destructive"`
	Idempotent  *bool `yaml:"idempotent"`
	OpenWorld   *bool `yaml:"open_world"`
}

type schemaSpec struct {
	Type                     string                `yaml:"type,omitempty"`
	JSONValue                bool                  `yaml:"json_value,omitempty"`
	Ref                      string                `yaml:"ref,omitempty"`
	Description              string                `yaml:"description,omitempty"`
	Properties               map[string]schemaSpec `yaml:"properties,omitempty"`
	Required                 *[]string             `yaml:"required,omitempty"`
	Items                    *schemaSpec           `yaml:"items,omitempty"`
	AdditionalProperties     *bool                 `yaml:"additional_properties,omitempty"`
	AdditionalPropertySchema *schemaSpec           `yaml:"additional_property_schema,omitempty"`
	Enum                     *[]any                `yaml:"enum,omitempty"`
	Pattern                  string                `yaml:"pattern,omitempty"`
	MinLength                *int                  `yaml:"min_length,omitempty"`
	MaxLength                *int                  `yaml:"max_length,omitempty"`
	Minimum                  *float64              `yaml:"minimum,omitempty"`
	Maximum                  *float64              `yaml:"maximum,omitempty"`
	MinItems                 *int                  `yaml:"min_items,omitempty"`
	MaxItems                 *int                  `yaml:"max_items,omitempty"`
	UniqueItems              *bool                 `yaml:"unique_items,omitempty"`
	Format                   string                `yaml:"format,omitempty"`
	Unit                     string                `yaml:"unit,omitempty"`
	Default                  yaml.Node             `yaml:"default,omitempty"`
	DefaultOverride          *defaultOverrideSpec  `yaml:"default_override,omitempty"`
	Const                    yaml.Node             `yaml:"const,omitempty"`
	AllOf                    *[]schemaSpec         `yaml:"all_of,omitempty"`
	OneOf                    *[]schemaSpec         `yaml:"one_of,omitempty"`
	AnyOf                    *[]schemaSpec         `yaml:"any_of,omitempty"`
	Not                      *schemaSpec           `yaml:"not,omitempty"`
	If                       *schemaSpec           `yaml:"if,omitempty"`
	Then                     *schemaSpec           `yaml:"then,omitempty"`
}

type defaultOverrideSpec struct {
	Value  yaml.Node `yaml:"value"`
	Reason string    `yaml:"reason"`
}

type AnnotationHints struct {
	ReadOnly    bool
	Destructive bool
	Idempotent  bool
	OpenWorld   bool
}

type ActionMetadata struct {
	Surface               string
	Selector              string
	Annotations           AnnotationHints
	HandlerInputMappings  []FieldMapping
	HandlerOutputMappings []FieldMapping
}

type CompiledSchema struct {
	Type                     string
	JSONValue                bool
	RefName                  string
	Description              string
	Properties               map[string]*CompiledSchema
	Required                 []string
	Items                    *CompiledSchema
	AdditionalProperties     *bool
	AdditionalPropertySchema *CompiledSchema
	Enum                     []any
	Pattern                  string
	MinLength                *int
	MaxLength                *int
	Minimum                  *float64
	Maximum                  *float64
	MinItems                 *int
	MaxItems                 *int
	UniqueItems              *bool
	Format                   string
	Unit                     string
	Default                  any
	HasDefault               bool
	DefaultOverrideReason    string
	Const                    any
	HasConst                 bool
	AllOf                    []*CompiledSchema
	OneOf                    []*CompiledSchema
	AnyOf                    []*CompiledSchema
	Not                      *CompiledSchema
	If                       *CompiledSchema
	Then                     *CompiledSchema
}

type CompiledAction struct {
	Path        string
	Description string
	Metadata    ActionMetadata
	Input       *CompiledSchema
	Output      *CompiledSchema
}

type CompiledSet struct {
	definitions        map[string]*CompiledSchema
	definitionMetadata map[string]definitionMetadata
	crossDomainFields  []string
	actions            map[string]CompiledAction
	paths              []string
}

type Discovery struct {
	Domains []string
	Actions []ActionSummary
}

type ActionSummary struct {
	Path        string         `json:"path"`
	Description string         `json:"description"`
	Input       map[string]any `json:"input"`
}
