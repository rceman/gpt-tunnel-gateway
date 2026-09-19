package model

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// JournalStream is the server-owned ADR56 rev2 stream identifier. The stream
// registry is closed: journal/add fails closed on any stream not listed in
// journalStreamRegistry, and each stream owns its writer authority and data
// contract.
type JournalStream string

const (
	JournalStreamPlannerNotes  JournalStream = "planner-notes"
	JournalStreamWorkerLessons JournalStream = "worker-lessons"
	JournalStreamLeadFriction  JournalStream = "lead-friction"
)

const (
	JournalStatusPublished = "published"

	MaxJournalStreamBytes     = 64
	MaxJournalDataBytes       = 16384
	MaxJournalStringBytes     = 4096
	MaxJournalArrayItems      = 64
	MaxJournalArrayItemBytes  = 2048
	MaxJournalIdentifierBytes = 128
	journalTaskRefPattern     = `^[A-Z]{3}-TSK(` + OperatorJournalNumberPattern + `)$`
)

var journalTaskRefRE = regexp.MustCompile(journalTaskRefPattern)

// JournalStreamContract is the published contract for one stream: purpose,
// closed data schema, writer authority, and bounds. journal/contract returns
// this projection verbatim.
type JournalStreamContract struct {
	Stream          string              `json:"stream"`
	Purpose         string              `json:"purpose"`
	DataSchema      map[string]any      `json:"data_schema"`
	Guide           string              `json:"guide"`
	WriterAuthority []string            `json:"writer_authority"`
	Limits          JournalStreamLimits `json:"limits"`
}

// JournalStreamLimits are the server-owned bounds applied to every entry in
// the stream before commit.
type JournalStreamLimits struct {
	MaxDataBytes      int `json:"max_data_bytes"`
	MaxStringBytes    int `json:"max_string_bytes"`
	MaxArrayItems     int `json:"max_array_items"`
	MaxArrayItemBytes int `json:"max_array_item_bytes"`
}

type journalStreamDefinition struct {
	Stream   JournalStream
	Purpose  string
	Guide    string
	Writers  []string
	Fields   []journalFieldSpec
	Required []string
}

type journalFieldSpec struct {
	Name      string
	Kind      journalFieldKind
	Enum      []string
	Optional  bool
	IsTaskRef bool
}

type journalFieldKind int

const (
	journalFieldString journalFieldKind = iota
	journalFieldStringArray
)

var journalStreamRegistry = map[JournalStream]journalStreamDefinition{
	JournalStreamPlannerNotes: {
		Stream:  JournalStreamPlannerNotes,
		Purpose: "Planner-owned structured note stream for planning context the project must keep durable.",
		Guide:   "Record bounded planning summaries, decisions, commitments, facts, assumptions, blockers, unresolved questions, next actions, and reference identifiers. Keep entries concise and factual.",
		Writers: []string{"planner"},
		Fields: []journalFieldSpec{
			{Name: "summary", Kind: journalFieldString},
			{Name: "decisions", Kind: journalFieldStringArray},
			{Name: "commitments", Kind: journalFieldStringArray},
			{Name: "facts", Kind: journalFieldStringArray},
			{Name: "assumptions", Kind: journalFieldStringArray},
			{Name: "blockers", Kind: journalFieldStringArray},
			{Name: "unresolved", Kind: journalFieldStringArray},
			{Name: "next_actions", Kind: journalFieldStringArray},
			{Name: "references", Kind: journalFieldStringArray},
		},
		Required: []string{"summary", "decisions", "commitments", "facts", "assumptions", "blockers", "unresolved", "next_actions", "references"},
	},
	JournalStreamWorkerLessons: {
		Stream:  JournalStreamWorkerLessons,
		Purpose: "Worker and Lead lesson stream: review and rework are primary lesson sources.",
		Guide:   "Record one concrete mistake, why it happened, the prevention, its scope, and evidence references. Lead reviews count as first-class lessons.",
		Writers: []string{"lead", "worker"},
		Fields: []journalFieldSpec{
			{Name: "task", Kind: journalFieldString, Optional: true, IsTaskRef: true},
			{Name: "mistake", Kind: journalFieldString},
			{Name: "why", Kind: journalFieldString},
			{Name: "prevention", Kind: journalFieldString},
			{Name: "scope", Kind: journalFieldString, Enum: []string{"universal", "project", "task-only", "tooling", "universal-positive"}},
			{Name: "evidence", Kind: journalFieldStringArray},
		},
		Required: []string{"mistake", "why", "prevention", "scope", "evidence"},
	},
	JournalStreamLeadFriction: {
		Stream:  JournalStreamLeadFriction,
		Purpose: "Lead-owned friction stream for review and orchestration problems worth fixing.",
		Guide:   "Record the problem, bounded evidence references, the impact, and an optional proposed improvement.",
		Writers: []string{"lead"},
		Fields: []journalFieldSpec{
			{Name: "problem", Kind: journalFieldString},
			{Name: "evidence", Kind: journalFieldStringArray},
			{Name: "impact", Kind: journalFieldString},
			{Name: "proposed_improvement", Kind: journalFieldString, Optional: true},
		},
		Required: []string{"problem", "evidence", "impact"},
	},
}

// JournalStreams returns the closed stream registry in deterministic order.
func JournalStreams() []JournalStream {
	return []JournalStream{JournalStreamPlannerNotes, JournalStreamWorkerLessons, JournalStreamLeadFriction}
}

// JournalStreamContractFor returns the published contract for one stream.
// Unsupported streams fail closed.
func JournalStreamContractFor(stream string) (JournalStreamContract, error) {
	definition, ok := journalStreamRegistry[JournalStream(stream)]
	if !ok {
		return JournalStreamContract{}, fmt.Errorf("unsupported journal stream %q", stream)
	}
	return JournalStreamContract{
		Stream:          string(definition.Stream),
		Purpose:         definition.Purpose,
		DataSchema:      journalStreamDataSchema(definition),
		Guide:           definition.Guide,
		WriterAuthority: append([]string(nil), definition.Writers...),
		Limits: JournalStreamLimits{
			MaxDataBytes:      MaxJournalDataBytes,
			MaxStringBytes:    MaxJournalStringBytes,
			MaxArrayItems:     MaxJournalArrayItems,
			MaxArrayItemBytes: MaxJournalArrayItemBytes,
		},
	}, nil
}

// JournalStreamWriterAllowed reports whether the durable Session role may
// append to the stream.
func JournalStreamWriterAllowed(stream JournalStream, role string) bool {
	definition, ok := journalStreamRegistry[stream]
	if !ok {
		return false
	}
	for _, writer := range definition.Writers {
		if writer == role {
			return true
		}
	}
	return false
}

func journalStreamDataSchema(definition journalStreamDefinition) map[string]any {
	properties := make(map[string]any, len(definition.Fields))
	for _, field := range definition.Fields {
		var schema map[string]any
		if field.Kind == journalFieldStringArray {
			schema = map[string]any{
				"type":     "array",
				"maxItems": MaxJournalArrayItems,
				"items":    map[string]any{"type": "string", "minLength": 1, "maxLength": MaxJournalArrayItemBytes},
			}
		} else {
			schema = map[string]any{"type": "string", "minLength": 1, "maxLength": MaxJournalStringBytes}
			if field.IsTaskRef {
				schema["pattern"] = journalTaskRefPattern
			}
			if len(field.Enum) > 0 {
				enum := make([]any, 0, len(field.Enum))
				for _, value := range field.Enum {
					enum = append(enum, value)
				}
				schema["enum"] = enum
			}
		}
		properties[field.Name] = schema
	}
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             append([]string(nil), definition.Required...),
		"additionalProperties": false,
	}
}

// JournalFieldViolation reports one contract violation at an exact data path.
type JournalFieldViolation struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (v JournalFieldViolation) Error() string {
	return v.Path + ": " + v.Message
}

// JournalEntry is the canonical ADR56 rev2 journal record stored in
// shared_journals. Entries are immutable, append-only, and published-only.
type JournalEntry struct {
	SchemaVersion int             `json:"schema_version"`
	ID            string          `json:"id"`
	ProjectID     string          `json:"project_id"`
	Status        string          `json:"status"`
	Stream        JournalStream   `json:"stream"`
	Data          json.RawMessage `json:"data"`
	Actor         string          `json:"actor"`
	Role          string          `json:"role"`
	SessionID     string          `json:"session"`
	Sequence      uint64          `json:"sequence"`
	CreatedAt     time.Time       `json:"created_at"`
}

// ValidateJournalStreamData enforces the closed stream data contract and
// returns structured violations with exact failing paths. On any violation
// the caller must write nothing.
func ValidateJournalStreamData(stream JournalStream, data json.RawMessage) []JournalFieldViolation {
	definition, ok := journalStreamRegistry[stream]
	violations := make([]JournalFieldViolation, 0, 4)
	if !ok {
		return append(violations, JournalFieldViolation{
			Path:    "stream",
			Message: fmt.Sprintf("unsupported journal stream %q", stream),
		})
	}
	if len(data) == 0 {
		return append(violations, JournalFieldViolation{
			Path:    "data",
			Message: "journal data is required",
		})
	}
	if len(data) > MaxJournalDataBytes {
		return append(violations, JournalFieldViolation{
			Path:    "data",
			Message: "journal data exceeds the bounded size",
		})
	}
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fields); err != nil {
		return append(violations, JournalFieldViolation{
			Path:    "data",
			Message: "journal data must be a closed object: " + err.Error(),
		})
	}
	if fields == nil {
		return append(violations, JournalFieldViolation{
			Path:    "data",
			Message: "journal data must be an object",
		})
	}
	allowed := make(map[string]journalFieldSpec, len(definition.Fields))
	for _, field := range definition.Fields {
		allowed[field.Name] = field
	}
	for name := range fields {
		if _, ok := allowed[name]; !ok {
			violations = append(violations, JournalFieldViolation{
				Path:    "data." + name,
				Message: "field is not part of the stream contract",
			})
		}
	}
	for _, field := range definition.Fields {
		raw, present := fields[field.Name]
		path := "data." + field.Name
		if !present || string(raw) == "null" {
			if !field.Optional {
				violations = append(violations, JournalFieldViolation{
					Path:    path,
					Message: "required field is missing",
				})
			}
			continue
		}
		switch field.Kind {
		case journalFieldString:
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				violations = append(violations, JournalFieldViolation{
					Path:    path,
					Message: "field must be a string",
				})
				continue
			}
			if strings.TrimSpace(value) == "" {
				violations = append(violations, JournalFieldViolation{
					Path:    path,
					Message: "field must be a non-empty string",
				})
				continue
			}
			if len(value) > MaxJournalStringBytes || strings.ContainsRune(value, 0) {
				violations = append(violations, JournalFieldViolation{
					Path:    path,
					Message: "field exceeds the bounded string size",
				})
				continue
			}
			if field.IsTaskRef && !journalTaskRefRE.MatchString(value) {
				violations = append(violations, JournalFieldViolation{
					Path:    path,
					Message: "field must be a canonical Task identifier",
				})
			}
			if len(field.Enum) > 0 {
				allowed := false
				for _, value2 := range field.Enum {
					if value == value2 {
						allowed = true
					}
				}
				if !allowed {
					violations = append(violations, JournalFieldViolation{
						Path:    path,
						Message: fmt.Sprintf("field must be one of %s", strings.Join(field.Enum, ",")),
					})
				}
			}
		case journalFieldStringArray:
			var values []string
			if err := json.Unmarshal(raw, &values); err != nil {
				violations = append(violations, JournalFieldViolation{
					Path:    path,
					Message: "field must be an array of strings",
				})
				continue
			}
			if values == nil {
				violations = append(violations, JournalFieldViolation{
					Path:    path,
					Message: "field must be an array",
				})
				continue
			}
			if len(values) > MaxJournalArrayItems {
				violations = append(violations, JournalFieldViolation{
					Path:    path,
					Message: "field exceeds the bounded item count",
				})
				continue
			}
			for index, value := range values {
				if strings.TrimSpace(value) == "" || len(value) > MaxJournalArrayItemBytes || strings.ContainsRune(value, 0) {
					violations = append(violations, JournalFieldViolation{
						Path:    fmt.Sprintf("%s[%d]", path, index),
						Message: "item must be a non-empty bounded string",
					})
				}
			}
		}
	}
	return violations
}

// ValidateJournalEntry validates the full canonical journal record.
func ValidateJournalEntry(entry JournalEntry) error {
	if entry.SchemaVersion != SchemaVersion || ValidateProjectIdentifier(entry.ProjectID) != nil || ValidateJournalID(entry.ID) != nil {
		return fmt.Errorf("invalid journal identity")
	}
	if entry.Status != JournalStatusPublished {
		return fmt.Errorf("invalid journal status")
	}
	if _, ok := journalStreamRegistry[entry.Stream]; !ok {
		return fmt.Errorf("unsupported journal stream %q", entry.Stream)
	}
	if violations := ValidateJournalStreamData(entry.Stream, entry.Data); len(violations) > 0 {
		return fmt.Errorf("invalid journal data: %s", violations[0].Error())
	}
	if entry.Actor == "" || len(entry.Actor) > MaxOperatorActorBytes || strings.TrimSpace(entry.Actor) != entry.Actor {
		return fmt.Errorf("invalid journal actor")
	}
	if entry.Role == "" || len(entry.Role) > MaxOperatorActorBytes {
		return fmt.Errorf("invalid journal role")
	}
	if entry.SessionID == "" || len(entry.SessionID) > MaxOperatorSessionIDBytes {
		return fmt.Errorf("invalid journal session")
	}
	if _, number, err := ParseJournalID(entry.ID); err != nil || number != entry.Sequence {
		return fmt.Errorf("journal sequence does not match the allocated identifier")
	}
	if entry.CreatedAt.IsZero() {
		return fmt.Errorf("invalid journal timestamp")
	}
	return nil
}
