package entity

import (
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type Family string

const (
	TaskFamily    Family = "TSK"
	ADRFamily     Family = "ADR"
	RuleFamily    Family = "RUL"
	MessageFamily Family = "MSG"
	TrainFamily   Family = "TRN"
	JournalFamily Family = "JRN"
)

type Descriptor struct {
	Family       Family
	Name         string
	Collection   string
	Suffix       string
	ProjectScope bool
	Order        string
	Fields       []string
	Default      []string
	Searchable   []string
	Filterable   []string
	Sortable     []string
	Operators    []string
}

var descriptorTable = map[Family]Descriptor{
	TaskFamily: {
		Family:       TaskFamily,
		Name:         "Task",
		Collection:   "tasks",
		Suffix:       ".json",
		ProjectScope: true,
		Order:        "id_asc",
		Fields:       []string{"id", "project_id", "title", "status", "operation_class", "created_at", "updated_at"},
		Default:      []string{"id", "title", "status"}, Searchable: []string{"id", "title", "status", "operation_class"},
		Filterable: []string{"id", "status", "operation_class"}, Sortable: []string{"id", "created_at", "updated_at", "status"}, Operators: []string{"=", "in", "contains"},
	},
	ADRFamily: {
		Family:       ADRFamily,
		Name:         "ADR",
		Collection:   "adrs",
		Suffix:       ".json",
		ProjectScope: true,
		Order:        "id_asc",
		Fields:       []string{"id", "project_id", "title", "status", "created_at", "updated_at"},
		Default:      []string{"id", "title", "status"}, Searchable: []string{"id", "title", "status"},
		Filterable: []string{"id", "status"}, Sortable: []string{"id", "created_at", "updated_at", "status"}, Operators: []string{"=", "in", "contains"},
	},
	RuleFamily: {
		Family:       RuleFamily,
		Name:         "Rule",
		Collection:   "rules",
		Suffix:       ".json",
		ProjectScope: true,
		Order:        "id_asc",
		Fields:       []string{"id", "project_id", "name", "description", "enabled", "created_at", "updated_at"},
		Default:      []string{"id", "name", "enabled"}, Searchable: []string{"id", "name", "description"},
		Filterable: []string{"id", "enabled"}, Sortable: []string{"id", "created_at", "updated_at", "name"}, Operators: []string{"=", "in", "contains"},
	},
	MessageFamily: {
		Family:       MessageFamily,
		Name:         "Message",
		Collection:   "messages",
		Suffix:       ".json",
		ProjectScope: true,
		Order:        "id_asc",
		Fields:       []string{"id", "project_id", "role", "content", "session_id", "created_at"},
		Default:      []string{"id", "role", "created_at"}, Searchable: []string{"id", "role", "content"},
		Filterable: []string{"id", "role", "session_id"}, Sortable: []string{"id", "created_at", "role"}, Operators: []string{"=", "in", "contains"},
	},
	TrainFamily: {
		Family:       TrainFamily,
		Name:         "Train",
		Collection:   "trains-v2",
		Suffix:       ".json",
		ProjectScope: true,
		Order:        "id_asc",
		Fields:       []string{"id", "project_id", "status", "revision", "created_at", "updated_at"},
		Default:      []string{"id", "status", "revision"}, Searchable: []string{"id", "status"},
		Filterable: []string{"id", "status"}, Sortable: []string{"id", "created_at", "updated_at", "revision", "status"}, Operators: []string{"=", "in"},
	},
	JournalFamily: {
		Family:       JournalFamily,
		Name:         "Journal",
		Collection:   "operator-journal/events",
		Suffix:       ".json",
		ProjectScope: true,
		Order:        "id_asc",
		Fields:       []string{"id", "project_id", "kind", "summary", "actor", "session_id", "occurred_at", "recorded_at"},
		Default:      []string{"id", "kind", "summary", "recorded_at"}, Searchable: []string{"id", "kind", "summary", "actor"},
		Filterable: []string{"id", "kind", "actor", "session_id"}, Sortable: []string{"id", "occurred_at", "recorded_at", "kind"}, Operators: []string{"=", "in", "contains"},
	},
}

func DescriptorFor(family Family) (Descriptor, error) {
	descriptor, ok := descriptorTable[family]
	if !ok {
		return Descriptor{}, fmt.Errorf("unsupported durable entity family %q", family)
	}
	return descriptor, nil
}

func Descriptors() []Descriptor {
	result := make([]Descriptor, 0, len(descriptorTable))
	for _, family := range []Family{TaskFamily, ADRFamily, RuleFamily, MessageFamily, TrainFamily, JournalFamily} {
		result = append(result, descriptorTable[family])
	}
	return result
}

func (d Descriptor) ValidateID(id string) error {
	if id == "" || id == "." || id == ".." || containsPathSeparator(id) {
		return fmt.Errorf("invalid %s identifier", d.Name)
	}
	switch d.Family {
	case TrainFamily:
		if _, _, err := model.ParseTrainV2ID(id); err != nil {
			return err
		}
	case RuleFamily:
		if err := model.ValidateRuleID(id); err != nil {
			return err
		}
	case MessageFamily:
		if err := model.ValidateMessageID(id); err != nil {
			return err
		}
	case JournalFamily:
		if err := model.ValidateAnyOperatorEventID(id); err != nil {
			return err
		}
	}
	return nil
}

func containsPathSeparator(value string) bool {
	for _, character := range value {
		if character == '/' || character == '\\' {
			return true
		}
	}
	return false
}
