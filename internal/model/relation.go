package model

import (
	"fmt"
	"regexp"
)

// Canonical relation kinds form a closed server-owned v1 enum. Arbitrary
// caller strings are rejected; additional kinds require owner approval.
const (
	RelationKindAuthority  = "authority"
	RelationKindCorrects   = "corrects"
	RelationKindSupersedes = "supersedes"
	RelationKindConcerns   = "concerns"
)

// Relation endpoint families. PMT is a Local-only family: PMT relation rows are
// persisted in the Local store and are never Shared/Hub replicated.
const (
	RelationFamilyTask = "TSK"
	RelationFamilyADR  = "ADR"
	RelationFamilyRule = "RUL"
	RelationFamilyPMT  = "PMT"
)

const relationPMTIDPattern = `^[A-Z]{3}-PMT(` + OperatorJournalNumberPattern + `)$`

var relationPMTIDRE = regexp.MustCompile(relationPMTIDPattern)

type relationEndpointRule struct {
	sources []string
	targets []string
}

var relationKindMatrix = map[string]relationEndpointRule{
	RelationKindAuthority:  {sources: []string{RelationFamilyTask, RelationFamilyADR, RelationFamilyRule}, targets: []string{RelationFamilyADR, RelationFamilyRule}},
	RelationKindCorrects:   {sources: []string{RelationFamilyTask}, targets: []string{RelationFamilyTask}},
	RelationKindSupersedes: {sources: []string{RelationFamilyADR}, targets: []string{RelationFamilyADR}},
	RelationKindConcerns:   {sources: []string{RelationFamilyPMT}, targets: []string{RelationFamilyTask, RelationFamilyADR, RelationFamilyRule}},
}

// RelationKinds returns the closed v1 kind enum in deterministic order.
func RelationKinds() []string {
	return []string{RelationKindAuthority, RelationKindCorrects, RelationKindSupersedes, RelationKindConcerns}
}

func ValidateRelationKind(kind string) error {
	if _, ok := relationKindMatrix[kind]; !ok {
		return fmt.Errorf("unsupported relation kind %q", kind)
	}
	return nil
}

// RelationFamilyOf resolves the canonical family token of a relation endpoint.
func RelationFamilyOf(id string) (string, error) {
	switch {
	case ValidateCanonicalTaskID(id) == nil:
		return RelationFamilyTask, nil
	case ValidateCanonicalADRIdentifier(id) == nil:
		return RelationFamilyADR, nil
	case ValidateRuleID(id) == nil:
		return RelationFamilyRule, nil
	case relationPMTIDRE.MatchString(id):
		return RelationFamilyPMT, nil
	default:
		return "", fmt.Errorf("unsupported relation endpoint %q", id)
	}
}

// RelationTitleField is the authoritative live-title field for a canonical
// target family. Relation state never stores a copy of these titles.
func RelationTitleField(family string) (string, error) {
	switch family {
	case RelationFamilyTask, RelationFamilyADR, RelationFamilyRule:
		return "title", nil
	default:
		return "", fmt.Errorf("relation family %q has no live-title field", family)
	}
}

// ValidateRelationKindFamilies enforces the closed kind enum and the approved
// source/target family matrix.
func ValidateRelationKindFamilies(kind, sourceFamily, targetFamily string) error {
	rule, ok := relationKindMatrix[kind]
	if !ok {
		return fmt.Errorf("unsupported relation kind %q", kind)
	}
	if !containsRelationFamily(rule.sources, sourceFamily) {
		return fmt.Errorf("relation kind %q does not accept a %s source", kind, sourceFamily)
	}
	if !containsRelationFamily(rule.targets, targetFamily) {
		return fmt.Errorf("relation kind %q does not accept a %s target", kind, targetFamily)
	}
	return nil
}

// ValidateRelationEndpoints enforces the closed kind enum and the approved
// source/target family matrix with one stored direction.
func ValidateRelationEndpoints(kind, source, target string) (string, string, error) {
	if source == target {
		return "", "", fmt.Errorf("relation source and target must differ")
	}
	sourceFamily, err := RelationFamilyOf(source)
	if err != nil {
		return "", "", fmt.Errorf("relation source: %w", err)
	}
	targetFamily, err := RelationFamilyOf(target)
	if err != nil {
		return "", "", fmt.Errorf("relation target: %w", err)
	}
	if err := ValidateRelationKindFamilies(kind, sourceFamily, targetFamily); err != nil {
		return "", "", err
	}
	return sourceFamily, targetFamily, nil
}

func containsRelationFamily(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
