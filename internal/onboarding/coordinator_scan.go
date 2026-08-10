package onboarding

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func decodeHubObject(data []byte, index int) (any, error) {
	var destination any
	switch index {
	case 0:
		destination = &model.Project{}
	case 1:
		destination = &model.Plan{}
	case 2:
		destination = &model.ProjectIdentifiers{}
	default:
		return nil, errors.New("invalid onboarding object index")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("hub object has trailing content: %w", err)
	}
	switch value := destination.(type) {
	case *model.Project:
		if err := model.ValidateProject(*value); err != nil {
			return nil, err
		}
		return *value, nil
	case *model.Plan:
		if err := model.ValidatePlan(*value); err != nil {
			return nil, err
		}
		return *value, nil
	case *model.ProjectIdentifiers:
		if err := model.ValidateProjectIdentifiers(*value); err != nil {
			return nil, err
		}
		return *value, nil
	}
	return nil, errors.New("invalid onboarding object")
}

func validateWorktreeTarget(worktree string, request Request, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers) error {
	sections, err := buildPlanSections(request)
	if err != nil {
		return err
	}
	objects := onboardingObjects(request, project, plan, identifiers, sections)
	paths := make([]string, 0, len(objects))
	for _, object := range objects {
		paths = append(paths, object.Path)
	}
	for _, path := range paths {
		if _, err := os.Lstat(filepath.Join(worktree, filepath.FromSlash(path))); err == nil {
			return fmt.Errorf("ONBOARDING_RECOVERY_REQUIRED: target path already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	entries, err := scanWorktreeRecords(worktree)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Project.ID == project.ID || entry.Project.RepositoryURL == project.RepositoryURL || (entry.Identifiers != nil && entry.Identifiers.ProjectCode == identifiers.ProjectCode) {
			return fmt.Errorf("ONBOARDING_RECOVERY_REQUIRED: durable project or project code collision")
		}
	}
	_ = plan
	return nil
}

func scanWorktreeRecords(worktree string) ([]worktreeRecord, error) {
	root := filepath.Join(worktree, "gpt-tunnel", "v1", "projects")
	projects := map[string]model.Project{}
	identifiers := map[string]model.ProjectIdentifiers{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		slash := filepath.ToSlash(path)
		isProject := strings.HasSuffix(slash, "/project.json")
		isIdentifiers := strings.HasSuffix(slash, "/identifiers.json")
		if !isProject && !isIdentifiers {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) != 2 || parts[0] == "" {
			return fmt.Errorf("non-canonical durable project path %q", relative)
		}
		pathProjectID := parts[0]
		if err := model.ValidateProjectIdentifier(pathProjectID); err != nil {
			return fmt.Errorf("invalid durable project path ID %q: %w", pathProjectID, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if isProject {
			var value model.Project
			if err := decodeStrictHubFile(data, &value); err != nil {
				return err
			}
			if err := model.ValidateProject(value); err != nil {
				return err
			}
			if value.ID != pathProjectID {
				return fmt.Errorf("durable project path ID %q does not match embedded project ID %q", pathProjectID, value.ID)
			}
			if _, exists := projects[pathProjectID]; exists {
				return fmt.Errorf("duplicate durable project ID %q", pathProjectID)
			}
			projects[pathProjectID] = value
		} else if isIdentifiers {
			var value model.ProjectIdentifiers
			if err := decodeStrictHubFile(data, &value); err != nil {
				return err
			}
			if err := model.ValidateProjectIdentifiers(value); err != nil {
				return err
			}
			if value.ProjectID != pathProjectID {
				return fmt.Errorf("durable identifiers path ID %q does not match embedded project ID %q", pathProjectID, value.ProjectID)
			}
			if _, exists := identifiers[pathProjectID]; exists {
				return fmt.Errorf("duplicate durable identifiers ID %q", pathProjectID)
			}
			identifiers[pathProjectID] = value
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for id := range identifiers {
		if _, ok := projects[id]; !ok {
			return nil, fmt.Errorf("durable identifiers %q are missing project", id)
		}
	}
	ids := make([]string, 0, len(projects))
	for id := range projects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]worktreeRecord, 0, len(ids))
	for _, id := range ids {
		var projectIdentifiers *model.ProjectIdentifiers
		if value, ok := identifiers[id]; ok {
			copy := value
			projectIdentifiers = &copy
		}
		result = append(result, worktreeRecord{
			Project:     projects[id],
			Identifiers: projectIdentifiers,
		})
	}
	return result, nil
}

func decodeStrictHubFile(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
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
