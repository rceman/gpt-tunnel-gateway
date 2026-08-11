package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

func (s *Service) projectPrefix(id string) string {
	if model.ValidateProjectIdentifier(id) != nil {
		return "../invalid-project-id"
	}
	return filepath.ToSlash(filepath.Join(hub.ProtocolRoot, "projects", id))
}

func (s *Service) projectPath(id string) string { return s.projectPrefix(id) + "/project.json" }

func (s *Service) planPath(id string) string { return s.projectPrefix(id) + "/plan/current.json" }

func (s *Service) projectIdentifiersPath(id string) string {
	if model.ValidateProjectIdentifier(id) != nil {
		return "../invalid-project-identifiers"
	}
	return s.projectPrefix(id) + "/identifiers.json"
}

func (s *Service) planSectionPath(project, id string) string {
	if model.ValidateObjectIdentifier(id) != nil {
		return "../invalid-plan-section-id"
	}
	return s.projectPrefix(project) + "/plan/sections/" + id + ".json"
}

func (s *Service) adrPath(project, id string) string {
	if model.ValidateADRIdentifier(id) != nil && model.ValidateCanonicalADRIdentifier(id) != nil {
		return "../invalid-adr-id"
	}
	return s.projectPrefix(project) + "/adrs/" + id + ".json"
}

func (s *Service) taskPath(project, id string) string {
	if model.ValidateObjectIdentifier(id) != nil {
		return "../invalid-task-id"
	}
	return s.projectPrefix(project) + "/tasks/" + id + ".json"
}

func (s *Service) taskAuthoringPath(project, id string) string {
	if model.ValidateProjectIdentifier(project) != nil || model.ValidateCanonicalTaskID(id) != nil {
		return "../invalid-task-authoring"
	}
	return s.projectPrefix(project) + "/tasks-v2/" + id + ".json"
}

func (s *Service) taskStatePath(project, id string) string {
	if model.ValidateObjectIdentifier(id) != nil {
		return "../invalid-task-id"
	}
	return s.projectPrefix(project) + "/tasks/" + id + ".state.json"
}

func (s *Service) taskIntegrationReceiptPath(project, id string) string {
	if model.ValidateObjectIdentifier(id) != nil {
		return "../invalid-task-id"
	}
	return s.projectPrefix(project) + "/tasks/" + id + ".integration-receipt.json"
}

func (s *Service) taskRunCounterPath(project, id string) string {
	if model.ValidateProjectIdentifier(project) != nil || model.ValidateCanonicalTaskID(id) != nil {
		return "../invalid-task-run-counter"
	}
	return s.projectPrefix(project) + "/tasks/" + id + ".run-counter.json"
}

func (s *Service) runPrefix(project, id string) string {
	if model.ValidateObjectIdentifier(id) != nil {
		return "../invalid-run-id"
	}
	return s.projectPrefix(project) + "/runs/" + id
}

func (s *Service) runPath(project, id string) string { return s.runPrefix(project, id) + "/run.json" }

func (s *Service) reportPath(project, id string) string {
	return s.runPrefix(project, id) + "/report.json"
}

func decodeStrict(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}

func readWorktreeJSON(worktree, path string, out any) error {
	data, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	return decodeStrict(data, out)
}

func ensureSessionAvailableInWorktree(worktree, session string, maxReadBytes int64) error {
	return ensureSessionAvailableInWorktreeForRun(worktree, session, "", "", maxReadBytes)
}

func ensureSessionAvailableInWorktreeForRun(worktree, session, trainID, laneBranch string, maxReadBytes int64) error {
	root := filepath.Join(worktree, filepath.FromSlash(hub.ProtocolRoot), "projects")
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "run.json" {
			return nil
		}
		data, err := fsutil.ReadFileBounded(path, maxReadBytes)
		if err != nil {
			return err
		}
		run, _, err := model.DecodeRunRecord(data)
		if err != nil {
			return fmt.Errorf("decode active run %s: %w", path, err)
		}
		if run.SessionKey == session && operationalActiveRun(run) && sessionRunCollides(run, trainID, laneBranch) {
			return fmt.Errorf("active operational run %s already owns the project session", run.ID)
		}
		return nil
	})
}

func (s *Service) projectConfig(id string) (config.ProjectConfig, error) {
	return s.EffectiveProjectConfig(id)
}

func (s *Service) hubRevision(ctx context.Context) (string, error) { return s.Hub.RemoteRevision(ctx) }

func (s *Service) ProjectList(ctx context.Context) ([]model.Project, error) {
	result, err := s.projectListAll(ctx)
	return result, err
}

func (s *Service) projectListAll(ctx context.Context) ([]model.Project, error) {
	paths, err := s.Hub.List(ctx, hub.ProtocolRoot+"/projects", "/project.json")
	if err != nil {
		return nil, err
	}
	items := []model.Project{}
	for _, path := range paths {
		var p model.Project
		if err := s.Hub.ReadJSON(ctx, path, &p); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (s *Service) ProjectListPage(ctx context.Context, in CollectionPageInput) (ProjectListPageResult, error) {
	limit, err := pagination.Limit(in.Limit, s.Config.MaxListItems)
	if err != nil {
		return ProjectListPageResult{}, err
	}
	items, err := s.projectListAll(ctx)
	if err != nil {
		return ProjectListPageResult{}, err
	}
	page, info, err := pagination.Page("project_list", items, limit, in.Cursor, func(item model.Project) string { return item.ID })
	if err != nil {
		return ProjectListPageResult{}, err
	}
	return ProjectListPageResult{
		Projects:   page,
		NextCursor: info.NextCursor,
		HasMore:    info.HasMore,
	}, nil
}

// ValidateConfiguredProjectRecords prevents a fresh deployment from reporting
// configured projects while the canonical hub has no durable project records.

func (s *Service) ValidateConfiguredProjectRecords(ctx context.Context) error {
	ids, _, err := s.effectiveProjectIDs()
	if err != nil {
		return fmt.Errorf("validate configured projects: %w", err)
	}
	items, err := s.ProjectList(ctx)
	if err != nil {
		return fmt.Errorf("validate durable project records: %w", err)
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		seen[item.ID] = true
	}
	missing := []string{}
	for _, id := range ids {
		if !seen[id] {
			missing = append(missing, id)
			continue
		}
		var plan model.Plan
		if err := s.Hub.ReadJSON(ctx, s.planPath(id), &plan); err != nil {
			return fmt.Errorf("durable hub plan missing or invalid for project %q: %w", id, err)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("durable hub project records missing: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (s *Service) ProjectRead(ctx context.Context, id string) (model.Project, error) {
	var p model.Project
	err := s.Hub.ReadJSON(ctx, s.projectPath(id), &p)
	return p, err
}

func (s *Service) ProjectIdentifiersRead(ctx context.Context, projectID string) (model.ProjectIdentifiers, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.ProjectIdentifiers{}, err
	}
	var identifiers model.ProjectIdentifiers
	if err := s.Hub.ReadJSON(ctx, s.projectIdentifiersPath(projectID), &identifiers); err != nil {
		return model.ProjectIdentifiers{}, err
	}
	if err := model.ValidateProjectIdentifiers(identifiers); err != nil {
		return model.ProjectIdentifiers{}, err
	}
	if identifiers.ProjectID != projectID {
		return model.ProjectIdentifiers{}, fmt.Errorf("project identifiers project_id mismatch")
	}
	return identifiers, nil
}
