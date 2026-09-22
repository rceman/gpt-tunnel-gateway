package model

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	MilestoneSchemaVersion    = SchemaVersion
	MilestonePlanned          = "planned"
	MilestoneActive           = "active"
	MilestoneCompleted        = "completed"
	MilestoneArchived         = "archived"
	MilestoneStatusPlanned    = MilestonePlanned
	MilestoneStatusActive     = MilestoneActive
	MilestoneStatusCompleted  = MilestoneCompleted
	MilestoneStatusArchived   = MilestoneArchived
	MilestoneMaxTasks         = 256
	MilestoneMaxRefs          = 32
	MilestoneMaxEvidenceBytes = 20000
)

type Milestone struct {
	SchemaVersion          int       `json:"schema_version"`
	ID                     string    `json:"id"`
	ProjectID              string    `json:"project_id"`
	Revision               int       `json:"revision"`
	Title                  string    `json:"title"`
	Summary                string    `json:"summary,omitempty"`
	Status                 string    `json:"status"`
	Tasks                  []string  `json:"tasks"`
	CompletionEvidence     string    `json:"completion_evidence,omitempty"`
	CompletionEvidenceRefs []string  `json:"completion_evidence_refs,omitempty"`
	CreatedBy              string    `json:"created_by"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedBy              string    `json:"updated_by"`
	UpdatedAt              time.Time `json:"updated_at"`
}

func FormatMilestoneID(projectCode string, number uint64) (string, error) {
	return formatEntityID(projectCode, "MIL", number)
}

func ParseMilestoneID(value string) (string, uint64, error) {
	return parseEntityID(value, "MIL")
}

func ValidateMilestoneID(value string) error {
	_, _, err := ParseMilestoneID(value)
	return err
}

func MilestoneStatuses() []string {
	return []string{MilestonePlanned, MilestoneActive, MilestoneCompleted, MilestoneArchived}
}

func MilestoneStatusSymbol(status string) (string, error) {
	switch status {
	case MilestonePlanned:
		return "○", nil
	case MilestoneActive:
		return "▶", nil
	case MilestoneCompleted:
		return "✓", nil
	case MilestoneArchived:
		return "◆", nil
	default:
		return "", fmt.Errorf("invalid milestone status %q", status)
	}
}

func ValidateMilestone(v Milestone) error {
	if v.SchemaVersion != MilestoneSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || ValidateMilestoneID(v.ID) != nil {
		return fmt.Errorf("invalid milestone identity")
	}
	if len([]rune(v.Title)) < 3 || len([]rune(v.Title)) > 128 || len([]rune(v.Summary)) > 256 {
		return fmt.Errorf("invalid milestone content")
	}
	if len(v.Tasks) > MilestoneMaxTasks {
		return fmt.Errorf("milestone task membership exceeds %d", MilestoneMaxTasks)
	}
	seen := make(map[string]struct{}, len(v.Tasks))
	for _, taskID := range v.Tasks {
		if ValidateCanonicalTaskID(taskID) != nil {
			return fmt.Errorf("invalid milestone task %q", taskID)
		}
		if _, exists := seen[taskID]; exists {
			return fmt.Errorf("duplicate milestone task %q", taskID)
		}
		seen[taskID] = struct{}{}
	}
	if !containsMilestoneStatus(v.Status) {
		return fmt.Errorf("invalid milestone status")
	}
	if v.Status != MilestoneCompleted && v.Status != MilestoneArchived && (v.CompletionEvidence != "" || len(v.CompletionEvidenceRefs) != 0) {
		return fmt.Errorf("non-completed milestone contains completion evidence")
	}
	if v.Status == MilestoneCompleted || v.Status == MilestoneArchived {
		if err := validateMilestoneEvidence(v.ProjectID, v.CompletionEvidence, v.CompletionEvidenceRefs); err != nil {
			return err
		}
	}
	if v.Revision < 1 || v.CreatedBy == "" || v.UpdatedBy == "" || strings.ContainsAny(v.CreatedBy+v.UpdatedBy, "\x00\r\n") || v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid milestone metadata")
	}
	return nil
}

func NewMilestone(projectID, id, title, summary string, tasks []string, createdBy string, now time.Time) (Milestone, error) {
	milestone := Milestone{
		SchemaVersion: MilestoneSchemaVersion,
		ID:            id,
		ProjectID:     projectID,
		Revision:      1,
		Title:         title,
		Summary:       summary,
		Status:        MilestonePlanned,
		Tasks:         CanonicalMilestoneTasks(tasks),
		CreatedBy:     createdBy,
		CreatedAt:     now.UTC(),
		UpdatedBy:     createdBy,
		UpdatedAt:     now.UTC(),
	}
	if err := ValidateMilestone(milestone); err != nil {
		return Milestone{}, err
	}
	return milestone, nil
}

// CanonicalMilestoneTasks returns the unordered membership representation used
// in durable Milestone payloads. Callers receive a detached sorted copy.
func CanonicalMilestoneTasks(tasks []string) []string {
	result := append([]string(nil), tasks...)
	sort.Strings(result)
	return result
}

func ValidateMilestoneEvidence(projectID, evidence string, refs []string) error {
	return validateMilestoneEvidence(projectID, evidence, refs)
}

func validateMilestoneEvidence(projectID, evidence string, refs []string) error {
	if strings.TrimSpace(evidence) == "" || len([]byte(evidence)) > MilestoneMaxEvidenceBytes || strings.ContainsRune(evidence, '\x00') {
		return fmt.Errorf("milestone evidence is required and bounded")
	}
	if len(refs) > MilestoneMaxRefs {
		return fmt.Errorf("milestone evidence references exceed %d", MilestoneMaxRefs)
	}
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if err := ValidateMilestoneEvidenceReference(ref, projectID); err != nil {
			return err
		}
		if _, exists := seen[ref]; exists {
			return fmt.Errorf("duplicate milestone evidence reference %q", ref)
		}
		seen[ref] = struct{}{}
	}
	return nil
}

func ValidateMilestoneEvidenceReference(ref, projectID string) error {
	if projectID != "" && ValidateProjectIdentifier(projectID) != nil {
		return fmt.Errorf("invalid milestone evidence project")
	}
	if ValidateObjectIdentifier(ref) != nil {
		return fmt.Errorf("invalid milestone evidence reference %q", ref)
	}
	code, err := projectCodeFromProjectIDReference(ref)
	if err != nil || code == "" {
		return fmt.Errorf("invalid milestone evidence reference %q", ref)
	}
	if strings.HasPrefix(ref, code+"-TSK") {
		if _, _, err := ParseTaskID(ref); err != nil {
			return fmt.Errorf("invalid milestone evidence reference %q", ref)
		}
	} else if strings.HasPrefix(ref, code+"-ADR") {
		if _, _, err := ParseADRID(ref); err != nil {
			return fmt.Errorf("invalid milestone evidence reference %q", ref)
		}
	} else if strings.HasPrefix(ref, code+"-RUL") {
		if _, _, err := ParseRuleID(ref); err != nil {
			return fmt.Errorf("invalid milestone evidence reference %q", ref)
		}
	} else if strings.HasPrefix(ref, code+"-JRN") {
		if _, _, err := ParseJournalID(ref); err != nil {
			return fmt.Errorf("invalid milestone evidence reference %q", ref)
		}
	} else if strings.HasPrefix(ref, code+"-OPR") {
		if _, _, err := ParseOperatorEventID(ref); err != nil {
			return fmt.Errorf("invalid milestone evidence reference %q", ref)
		}
	} else if strings.HasPrefix(ref, code+"-MIL") {
		if _, _, err := ParseMilestoneID(ref); err != nil {
			return fmt.Errorf("invalid milestone evidence reference %q", ref)
		}
	} else if strings.HasPrefix(ref, code+"-MSG") {
		if _, _, err := ParseMessageID(ref); err != nil {
			return fmt.Errorf("invalid milestone evidence reference %q", ref)
		}
	} else {
		return fmt.Errorf("invalid milestone evidence reference %q", ref)
	}
	return nil
}

func projectCodeFromProjectIDReference(ref string) (string, error) {
	if len(ref) < 5 || ref[3] != '-' {
		return "", fmt.Errorf("invalid entity reference")
	}
	code := ref[:3]
	if err := ValidateProjectCode(code); err != nil {
		return "", err
	}
	return code, nil
}

func containsMilestoneStatus(status string) bool {
	for _, value := range MilestoneStatuses() {
		if value == status {
			return true
		}
	}
	return false
}
