package onboarding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func buildDurableObjects(request Request) (model.Project, model.Plan, model.ProjectIdentifiers, objectDigests, error) {
	updatedAt, err := parseReceiptTime(request.InitialPlan.UpdatedAt)
	if err != nil {
		return model.Project{}, model.Plan{}, model.ProjectIdentifiers{}, objectDigests{}, err
	}
	project := model.Project{SchemaVersion: model.SchemaVersion, ID: request.ProjectID, RepositoryURL: request.RepositoryURL, DefaultBranch: request.DefaultBranch, Status: "active", CreatedAt: updatedAt, UpdatedAt: updatedAt}
	if request.Workflow != nil {
		project.WorkflowRepository = request.Workflow.Repository
		project.WorkflowCommit = request.Workflow.Commit
	}
	plan := model.Plan{SchemaVersion: model.PlanSchemaVersion, ProjectID: request.ProjectID, Revision: int(request.InitialPlan.Revision), Title: request.InitialPlan.Title, Summary: request.InitialPlan.Summary, CurrentObjective: request.InitialPlan.CurrentObjective, Queue: append([]string(nil), request.InitialPlan.Queue...), UpdatedBy: request.InitialPlan.UpdatedBy, UpdatedAt: updatedAt}
	for _, section := range request.InitialPlan.Sections {
		plan.Sections = append(plan.Sections, model.PlanSectionIndex{ID: section.ID, Title: section.Title, ShortDescription: section.ShortDescription, Revision: int(section.Revision)})
	}
	identifiers := model.ProjectIdentifiers{SchemaVersion: model.SchemaVersion, ProjectID: request.ProjectID, ProjectCode: request.ProjectCode, NextTaskNumber: 1, NextADRNumber: 1}
	if err := model.ValidateProject(project); err != nil {
		return model.Project{}, model.Plan{}, model.ProjectIdentifiers{}, objectDigests{}, err
	}
	if err := model.ValidatePlan(plan); err != nil {
		return model.Project{}, model.Plan{}, model.ProjectIdentifiers{}, objectDigests{}, err
	}
	if err := model.ValidateProjectIdentifiers(identifiers); err != nil {
		return model.Project{}, model.Plan{}, model.ProjectIdentifiers{}, objectDigests{}, err
	}
	projectDigest, err := digestObject(project)
	if err != nil {
		return model.Project{}, model.Plan{}, model.ProjectIdentifiers{}, objectDigests{}, err
	}
	planDigest, err := digestObject(plan)
	if err != nil {
		return model.Project{}, model.Plan{}, model.ProjectIdentifiers{}, objectDigests{}, err
	}
	identifiersDigest, err := digestObject(identifiers)
	if err != nil {
		return model.Project{}, model.Plan{}, model.ProjectIdentifiers{}, objectDigests{}, err
	}
	return project, plan, identifiers, objectDigests{
		project:     projectDigest,
		plan:        planDigest,
		identifiers: identifiersDigest,
	}, nil
}

func digestObject(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func canonicalOnboardingPaths(projectID string) []string {
	return []string{
		fmt.Sprintf("gpt-tunnel/v1/projects/%s/project.json", projectID),
		fmt.Sprintf("gpt-tunnel/v1/projects/%s/plan/current.json", projectID),
		fmt.Sprintf("gpt-tunnel/v1/projects/%s/identifiers.json", projectID),
	}
}

func (c *Coordinator) inspectTarget(ctx context.Context, request Request, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers) (string, targetState, string, error) {
	paths := canonicalOnboardingPaths(request.ProjectID)
	collision, err := c.remoteCollision(ctx, request)
	if err != nil {
		return "", targetStateConflict, "", onboardingRecoveryError(err)
	}
	if collision {
		return "", targetStateConflict, "", errors.New("ONBOARDING_RECOVERY_REQUIRED: repository or project code collision")
	}
	present := 0
	exact := 0
	for index, path := range paths {
		data, err := c.Hub.ReadFile(ctx, path)
		if err != nil {
			if isHubNotFound(err) {
				continue
			}
			return "", targetStateConflict, "", onboardingRecoveryError(err)
		}
		present++
		var value any
		switch index {
		case 0:
			value = project
		case 1:
			value = plan
		case 2:
			value = identifiers
		}
		decoded, err := decodeHubObject(data, index)
		if err != nil {
			return "", targetStateConflict, "", onboardingRecoveryError(err)
		}
		canonical, err := json.MarshalIndent(decoded, "", "  ")
		if err != nil {
			return "", targetStateConflict, "", onboardingRecoveryError(err)
		}
		canonical = append(canonical, '\n')
		if !bytes.Equal(data, canonical) {
			return "", targetStateConflict, "", errors.New("target durable object is not canonical")
		}
		want, err := digestObject(value)
		if err != nil {
			return "", targetStateConflict, "", onboardingRecoveryError(err)
		}
		have, err := digestObject(decoded)
		if err == nil && want == have {
			exact++
		}
	}
	if present == 0 {
		revision, err := c.Hub.RemoteRevision(ctx)
		if err != nil {
			return "", targetStateConflict, "", onboardingRecoveryError(err)
		}
		return revision, targetStateEmpty, "", nil
	}
	if present == len(paths) && exact == len(paths) {
		revision, err := c.Hub.RemoteRevision(ctx)
		if err != nil {
			return "", targetStateConflict, "", onboardingRecoveryError(err)
		}
		after, err := c.commonPathLastChange(ctx, request.ProjectID)
		if err != nil {
			return "", targetStateConflict, "", onboardingRecoveryError(err)
		}
		return revision, targetStateExact, after, nil
	}
	return "", targetStateConflict, "", nil
}

func isHubNotFound(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "does not exist") || strings.Contains(message, "pathspec") || strings.Contains(message, "not found") || strings.Contains(message, "fatal: path")
}

func (c *Coordinator) commonPathLastChange(ctx context.Context, projectID string) (string, error) {
	paths := canonicalOnboardingPaths(projectID)
	var common string
	for _, path := range paths {
		lastChange, err := c.Hub.LastChange(ctx, path)
		if err != nil {
			return "", err
		}
		if common == "" {
			common = lastChange
			continue
		}
		if common != lastChange {
			return "", fmt.Errorf("onboarding paths have different last-change commits: %s versus %s", common, lastChange)
		}
	}
	if common == "" {
		return "", errors.New("onboarding paths have no common last-change commit")
	}
	return common, nil
}

func (c *Coordinator) validateCommittedHubState(ctx context.Context, request Request, receipt Receipt, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers, digests objectDigests) error {
	objects := []any{project, plan, identifiers}
	for index, path := range canonicalOnboardingPaths(request.ProjectID) {
		data, err := c.Hub.ReadFile(ctx, path)
		if err != nil {
			return fmt.Errorf("read committed onboarding object %s: %w", path, err)
		}
		decoded, err := decodeHubObject(data, index)
		if err != nil {
			return fmt.Errorf("decode committed onboarding object %s: %w", path, err)
		}
		canonical, err := json.MarshalIndent(decoded, "", "  ")
		if err != nil {
			return err
		}
		canonical = append(canonical, '\n')
		if !bytes.Equal(data, canonical) {
			return fmt.Errorf("committed onboarding object %s is not canonical", path)
		}
		want := []string{digests.project, digests.plan, digests.identifiers}[index]
		have, err := digestObject(decoded)
		if err != nil || have != want {
			return fmt.Errorf("committed onboarding object %s digest does not match receipt", path)
		}
		if !objectsMatch(decoded, objects[index]) {
			return fmt.Errorf("committed onboarding object %s does not match request", path)
		}
	}
	lastChange, err := c.commonPathLastChange(ctx, request.ProjectID)
	if err != nil {
		return err
	}
	if receipt.Hub.After == nil || lastChange != *receipt.Hub.After {
		return fmt.Errorf("committed onboarding last-change commit %s does not match recorded hub.after", lastChange)
	}
	return nil
}

func objectsMatch(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func receiptHubTransaction(receipt Receipt, store hub.Store) hub.TransactionResult {
	after := ""
	if receipt.Hub.After != nil {
		after = *receipt.Hub.After
	}
	return hub.TransactionResult{Before: receipt.Hub.Before, After: after, Remote: hub.RemoteName, Branch: store.Config.Hub.Branch, Paths: append([]string(nil), receipt.Hub.Paths...)}
}
