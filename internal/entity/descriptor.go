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
}

var descriptorTable = map[Family]Descriptor{
	TaskFamily: {
		Family:       TaskFamily,
		Name:         "Task",
		Collection:   "tasks",
		Suffix:       ".json",
		ProjectScope: true,
		Order:        "id_asc",
	},
	ADRFamily: {
		Family:       ADRFamily,
		Name:         "ADR",
		Collection:   "adrs",
		Suffix:       ".json",
		ProjectScope: true,
		Order:        "id_asc",
	},
	RuleFamily: {
		Family:       RuleFamily,
		Name:         "Rule",
		Collection:   "rules",
		Suffix:       ".json",
		ProjectScope: true,
		Order:        "id_asc",
	},
	MessageFamily: {
		Family:       MessageFamily,
		Name:         "Message",
		Collection:   "messages",
		Suffix:       ".json",
		ProjectScope: true,
		Order:        "id_asc",
	},
	TrainFamily: {
		Family:       TrainFamily,
		Name:         "Train",
		Collection:   "trains-v2",
		Suffix:       ".json",
		ProjectScope: true,
		Order:        "id_asc",
	},
	JournalFamily: {
		Family:       JournalFamily,
		Name:         "Journal",
		Collection:   "operator-journal/events",
		Suffix:       ".json",
		ProjectScope: true,
		Order:        "id_asc",
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
