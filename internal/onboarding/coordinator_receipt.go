package onboarding

import (
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func committedReceipt(prepared Receipt, request Request, after string, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers, transaction bool) Receipt {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	preparedAt := prepared.Timestamps.PreparedAt
	return Receipt{
		SchemaVersion:   prepared.SchemaVersion,
		OperationID:     prepared.OperationID,
		RequestSHA256:   prepared.RequestSHA256,
		State:           StateHubCommitted,
		ProjectID:       prepared.ProjectID,
		RepositoryProof: prepared.RepositoryProof,
		WorktreeProof:   prepared.WorktreeProof,
		SessionProof:    prepared.SessionProof,
		RegistryDigests: prepared.RegistryDigests,
		Hub: HubProof{
			Before: prepared.Hub.Before,
			After:  &after,
			Paths:  append([]string(nil), prepared.Hub.Paths...),
		},
		CreatedProject: &CreatedProject{
			ProjectID:          project.ID,
			RepositoryURL:      project.RepositoryURL,
			DefaultBranch:      project.DefaultBranch,
			Status:             project.Status,
			WorkflowRepository: optionalString(project.WorkflowRepository),
			WorkflowCommit:     optionalString(project.WorkflowCommit),
		},
		CreatedPlan: &CreatedPlan{
			SchemaVersion: PositiveInteger(plan.SchemaVersion),
			ProjectID:     plan.ProjectID,
			Revision:      PositiveInteger(plan.Revision),
			Path:          canonicalOnboardingPaths(request.ProjectID)[1],
		},
		CreatedIdentifiers: &CreatedIdentifiers{
			SchemaVersion:  PositiveInteger(identifiers.SchemaVersion),
			ProjectID:      identifiers.ProjectID,
			ProjectCode:    identifiers.ProjectCode,
			NextTaskNumber: PositiveInteger(identifiers.NextTaskNumber),
			NextADRNumber:  PositiveInteger(identifiers.NextADRNumber),
		},
		Timestamps: Timestamps{
			StartedAt:      prepared.Timestamps.StartedAt,
			UpdatedAt:      now,
			PreparedAt:     preparedAt,
			HubCommittedAt: stringPointer(now),
		},
		Recovery: Recovery{
			Status: "not_required",
		},
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringPointer(value string) *string { return &value }
