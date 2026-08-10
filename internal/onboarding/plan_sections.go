package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type onboardingObject struct {
	Path  string
	Value any
}

func (c *Coordinator) ensureCommittedPlanSections(ctx context.Context, request Request, receipt Receipt, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers, digests objectDigests) error {
	sections, err := buildPlanSections(request)
	if err != nil {
		return err
	}
	if err := c.validateCommittedPrimaryHubState(ctx, request, receipt, project, plan, identifiers, digests); err != nil {
		return err
	}
	if err := c.repairMissingPlanSections(ctx, request, project, plan, identifiers, sections); err != nil {
		return err
	}
	return c.validateCommittedHubState(ctx, request, receipt, project, plan, identifiers, digests)
}

func buildPlanSections(request Request) ([]model.PlanSection, error) {
	updatedAt, err := parseReceiptTime(request.InitialPlan.UpdatedAt)
	if err != nil {
		return nil, err
	}
	sections := make([]model.PlanSection, 0, len(request.InitialPlan.Sections))
	for _, input := range request.InitialPlan.Sections {
		section := model.PlanSection{
			SchemaVersion:    model.PlanSchemaVersion,
			ProjectID:        request.ProjectID,
			ID:               input.ID,
			Revision:         int(input.Revision),
			Title:            input.Title,
			ShortDescription: input.ShortDescription,
			UpdatedBy:        request.InitialPlan.UpdatedBy,
			UpdatedAt:        updatedAt,
		}
		if err := model.ValidatePlanSection(section); err != nil {
			return nil, fmt.Errorf("initial plan section %q: %w", input.ID, err)
		}
		sections = append(sections, section)
	}
	return sections, nil
}

func onboardingSectionPath(projectID, sectionID string) string {
	return fmt.Sprintf("gpt-tunnel/v1/projects/%s/plan/sections/%s.json", projectID, sectionID)
}

func onboardingObjects(request Request, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers, sections []model.PlanSection) []onboardingObject {
	objects := []onboardingObject{
		{Path: canonicalOnboardingPaths(request.ProjectID)[0], Value: project},
		{Path: canonicalOnboardingPaths(request.ProjectID)[1], Value: plan},
		{Path: canonicalOnboardingPaths(request.ProjectID)[2], Value: identifiers},
	}
	for _, section := range sections {
		objects = append(objects, onboardingObject{
			Path:  onboardingSectionPath(request.ProjectID, section.ID),
			Value: section,
		})
	}
	return objects
}

func writeOnboardingObjects(worktree string, objects []onboardingObject) ([]string, error) {
	paths := make([]string, 0, len(objects))
	for _, object := range objects {
		if err := hub.WriteJSON(worktree, object.Path, object.Value); err != nil {
			return nil, err
		}
		paths = append(paths, object.Path)
	}
	return paths, nil
}

func decodeOnboardingObject(data []byte, want any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(want); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("hub object has trailing content")
		}
		return fmt.Errorf("hub object has trailing content: %w", err)
	}
	return nil
}

func (c *Coordinator) repairMissingPlanSections(ctx context.Context, request Request, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers, sections []model.PlanSection) error {
	if len(sections) == 0 {
		return nil
	}
	expected, err := c.Hub.RemoteRevision(ctx)
	if err != nil {
		return err
	}
	objects := onboardingObjects(request, project, plan, identifiers, sections)
	missing := false
	for _, object := range objects[3:] {
		data, readErr := c.Hub.ReadFile(ctx, object.Path)
		if readErr != nil {
			if isHubNotFound(readErr) {
				missing = true
				continue
			}
			return readErr
		}
		if err := validateOnboardingObjectBytes(data, object.Value); err != nil {
			return fmt.Errorf("existing plan section %s conflicts with onboarding evidence: %w", object.Path, err)
		}
	}
	if !missing {
		return nil
	}
	_, err = c.Hub.Transact(ctx, expected, "gateway: materialize onboarding plan sections "+request.ProjectID, func(worktree string) ([]string, error) {
		var currentPlan model.Plan
		if err := readOnboardingWorktreeJSON(worktree, canonicalOnboardingPaths(request.ProjectID)[1], &currentPlan); err != nil {
			return nil, err
		}
		if !objectsMatch(currentPlan, plan) {
			return nil, errors.New("onboarding plan changed before section materialization")
		}
		for _, object := range objects[:3] {
			if err := validateOnboardingWorktreeObject(worktree, object); err != nil {
				return nil, err
			}
		}
		paths := make([]string, 0, len(sections))
		for _, object := range objects[3:] {
			path := filepath.Join(worktree, filepath.FromSlash(object.Path))
			if data, readErr := os.ReadFile(path); readErr == nil {
				if err := validateOnboardingObjectBytes(data, object.Value); err != nil {
					return nil, fmt.Errorf("existing plan section %s conflicts with onboarding evidence: %w", object.Path, err)
				}
				continue
			} else if !os.IsNotExist(readErr) {
				return nil, readErr
			}
			if err := hub.WriteJSON(worktree, object.Path, object.Value); err != nil {
				return nil, err
			}
			paths = append(paths, object.Path)
		}
		return paths, nil
	})
	return err
}

func readOnboardingWorktreeJSON(worktree, path string, destination any) error {
	data, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	return decodeOnboardingObject(data, destination)
}

func validateOnboardingWorktreeObject(worktree string, object onboardingObject) error {
	data, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(object.Path)))
	if err != nil {
		return err
	}
	return validateOnboardingObjectBytes(data, object.Value)
}

func validateOnboardingObjectBytes(data []byte, expected any) error {
	actual := cloneOnboardingValue(expected)
	if err := decodeOnboardingObject(data, actual); err != nil {
		return err
	}
	switch value := actual.(type) {
	case *model.Project:
		if err := model.ValidateProject(*value); err != nil {
			return err
		}
	case *model.Plan:
		if err := model.ValidatePlan(*value); err != nil {
			return err
		}
	case *model.ProjectIdentifiers:
		if err := model.ValidateProjectIdentifiers(*value); err != nil {
			return err
		}
	case *model.PlanSection:
		if err := model.ValidatePlanSection(*value); err != nil {
			return err
		}
	}
	canonical, err := json.MarshalIndent(actual, "", "  ")
	if err != nil {
		return err
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(data, canonical) {
		return errors.New("object is not canonical")
	}
	if !objectsMatch(actual, expected) {
		return errors.New("object does not match onboarding evidence")
	}
	return nil
}

func cloneOnboardingValue(value any) any {
	switch value.(type) {
	case model.Project:
		return &model.Project{}
	case model.Plan:
		return &model.Plan{}
	case model.ProjectIdentifiers:
		return &model.ProjectIdentifiers{}
	case model.PlanSection:
		return &model.PlanSection{}
	default:
		return &struct{}{}
	}
}
