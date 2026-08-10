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
	if _, err := buildPlanSections(request); err != nil {
		return model.Project{}, model.Plan{}, model.ProjectIdentifiers{}, objectDigests{}, err
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
	sections, err := buildPlanSections(request)
	if err != nil {
		return "", targetStateConflict, "", err
	}
	objects := onboardingObjects(request, project, plan, identifiers, sections)
	collision, err := c.remoteCollision(ctx, request)
	if err != nil {
		return "", targetStateConflict, "", onboardingRecoveryError(err)
	}
	if collision {
		return "", targetStateConflict, "", errors.New("ONBOARDING_RECOVERY_REQUIRED: repository or project code collision")
	}
	present := 0
	exact := 0
	for _, object := range objects {
		data, err := c.Hub.ReadFile(ctx, object.Path)
		if err != nil {
			if isHubNotFound(err) {
				continue
			}
			return "", targetStateConflict, "", onboardingRecoveryError(err)
		}
		present++
		if err := validateOnboardingObjectBytes(data, object.Value); err != nil {
			return "", targetStateConflict, "", onboardingRecoveryError(fmt.Errorf("target durable object %s: %w", object.Path, err))
		}
		exact++
	}
	if present == 0 {
		revision, err := c.Hub.RemoteRevision(ctx)
		if err != nil {
			return "", targetStateConflict, "", onboardingRecoveryError(err)
		}
		return revision, targetStateEmpty, "", nil
	}
	if present == len(objects) && exact == len(objects) {
		revision, err := c.Hub.RemoteRevision(ctx)
		if err != nil {
			return "", targetStateConflict, "", onboardingRecoveryError(err)
		}
		after, err := c.commonPathLastChange(ctx, request)
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

func (c *Coordinator) commonPathLastChange(ctx context.Context, request Request) (string, error) {
	sections, err := buildPlanSections(request)
	if err != nil {
		return "", err
	}
	project, plan, identifiers, _, err := buildDurableObjects(request)
	if err != nil {
		return "", err
	}
	objects := onboardingObjects(request, project, plan, identifiers, sections)
	var common string
	for _, object := range objects {
		lastChange, err := c.Hub.LastChange(ctx, object.Path)
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

func (c *Coordinator) validateCommittedPrimaryHubState(ctx context.Context, request Request, receipt Receipt, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers, digests objectDigests) error {
	objects := onboardingObjects(request, project, plan, identifiers, nil)
	for index, object := range objects {
		data, err := c.Hub.ReadFile(ctx, object.Path)
		if err != nil {
			return fmt.Errorf("read committed onboarding object %s: %w", object.Path, err)
		}
		if err := validateOnboardingObjectBytes(data, object.Value); err != nil {
			return fmt.Errorf("committed onboarding object %s: %w", object.Path, err)
		}
		actual := cloneOnboardingValue(object.Value)
		if err := decodeOnboardingObject(data, actual); err != nil {
			return fmt.Errorf("decode committed onboarding object %s: %w", object.Path, err)
		}
		want := []string{digests.project, digests.plan, digests.identifiers}[index]
		have, err := digestObject(actual)
		if err != nil || have != want {
			return fmt.Errorf("committed onboarding object %s digest does not match receipt", object.Path)
		}
	}
	if receipt.Hub.After == nil {
		return errors.New("committed onboarding receipt requires hub.after")
	}
	return nil
}

func (c *Coordinator) validateCommittedHubState(ctx context.Context, request Request, receipt Receipt, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers, digests objectDigests) error {
	sections, err := buildPlanSections(request)
	if err != nil {
		return err
	}
	objects := onboardingObjects(request, project, plan, identifiers, sections)
	for index, object := range objects {
		data, err := c.Hub.ReadFile(ctx, object.Path)
		if err != nil {
			return fmt.Errorf("read committed onboarding object %s: %w", object.Path, err)
		}
		if err := validateOnboardingObjectBytes(data, object.Value); err != nil {
			return fmt.Errorf("committed onboarding object %s: %w", object.Path, err)
		}
		if index < 3 {
			want := []string{digests.project, digests.plan, digests.identifiers}[index]
			actual := cloneOnboardingValue(object.Value)
			if err := decodeOnboardingObject(data, actual); err != nil {
				return fmt.Errorf("decode committed onboarding object %s: %w", object.Path, err)
			}
			have, err := digestObject(actual)
			if err != nil || have != want {
				return fmt.Errorf("committed onboarding object %s digest does not match receipt", object.Path)
			}
		}
	}
	if receipt.State == StateActivated || receipt.State == StateHubCommitted {
		return nil
	}
	lastChange, err := c.commonPathLastChange(ctx, request)
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
