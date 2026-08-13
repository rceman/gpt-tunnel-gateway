package model

import (
	"fmt"
)

func (p Plan) StatusView() PlanStatus {
	sections := make([]string, 0, len(p.Sections))
	for _, section := range p.Sections {
		sections = append(sections, fmt.Sprintf("* %s - %s", section.Title, section.ShortDescription))
	}
	return PlanStatus{
		SchemaVersion:    p.SchemaVersion,
		ProjectID:        p.ProjectID,
		Revision:         p.Revision,
		Title:            p.Title,
		Summary:          p.Summary,
		CurrentObjective: p.CurrentObjective,
		Queue:            append([]string{}, p.Queue...),
		Sections:         sections,
		ActiveTaskID:     p.ActiveTaskID,
		UpdatedBy:        p.UpdatedBy,
		UpdatedAt:        p.UpdatedAt,
	}
}
