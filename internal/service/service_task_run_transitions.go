package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

func (s *Service) transitionTaskStateWithWorktree(ctx context.Context, task model.Task, expected, subject string, mutate func(string, model.TaskState) (model.TaskState, error)) (hub.TransactionResult, error) {
	if expected == "" {
		var err error
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return hub.TransactionResult{}, err
		}
	}
	return s.Hub.Transact(ctx, expected, subject, func(worktree string) ([]string, error) {
		var currentTask model.Task
		if err := readWorktreeJSON(worktree, s.taskPath(task.ProjectID, task.ID), &currentTask); err != nil {
			return nil, err
		}
		if err := model.ValidateTask(currentTask); err != nil {
			return nil, err
		}
		if currentTask.ID != task.ID || currentTask.SHA256 != task.SHA256 {
			return nil, fmt.Errorf("task changed concurrently")
		}
		var current model.TaskState
		if err := readWorktreeJSON(worktree, s.taskStatePath(task.ProjectID, task.ID), &current); err != nil {
			return nil, err
		}
		if err := model.ValidateTaskState(current, currentTask); err != nil {
			return nil, err
		}
		next, err := mutate(worktree, current)
		if err != nil {
			return nil, err
		}
		next.UpdatedAt = time.Now().UTC()
		if err := model.ValidateTaskState(next, currentTask); err != nil {
			return nil, err
		}
		path := s.taskStatePath(task.ProjectID, task.ID)
		if err := hub.WriteJSON(worktree, path, next); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
}

func (s *Service) mirrorRemoteBranchHead(ctx context.Context, project config.ProjectConfig, branch string) (string, bool, error) {
	return s.Git.MirrorBranchHead(ctx, project, branch)
}

func (s *Service) RunList(ctx context.Context, project string) ([]model.Run, error) {
	paths, err := s.Hub.List(ctx, s.projectPrefix(project)+"/runs", "/run.json")
	if err != nil {
		return nil, err
	}
	items := []model.Run{}
	for _, path := range paths {
		data, err := s.Hub.ReadFile(ctx, path)
		if err != nil {
			return nil, err
		}
		v, _, err := model.DecodeRunRecord(data)
		if err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func (s *Service) RunListPage(ctx context.Context, project string, in CollectionPageInput) (RunListPageResult, error) {
	limit, err := pagination.Limit(in.Limit, s.Config.MaxListItems)
	if err != nil {
		return RunListPageResult{}, err
	}
	items, err := s.RunList(ctx, project)
	if err != nil {
		return RunListPageResult{}, err
	}
	page, info, err := pagination.Page("run_list:"+project, items, limit, in.Cursor, func(item model.Run) string {
		return item.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + item.ID
	})
	if err != nil {
		return RunListPageResult{}, err
	}
	return RunListPageResult{
		Runs:       page,
		NextCursor: info.NextCursor,
		HasMore:    info.HasMore,
	}, nil
}

func latestApplicableRunForRevision(runs []model.Run, taskID string, revision int, revisionSHA string) (model.Run, bool) {
	for _, run := range runs {
		if run.TaskID != taskID || run.Historical {
			continue
		}
		runRevision := run.TaskRevision
		if runRevision == 0 {
			runRevision = 1
		}
		if runRevision != revision {
			continue
		}
		if revision > 1 && run.TaskRevisionSHA256 != revisionSHA {
			continue
		}
		if revision == 1 && run.TaskRevision != 0 && run.TaskRevisionSHA256 != revisionSHA {
			continue
		}
		if run.TaskID == taskID && !run.Historical {
			return run, true
		}
	}
	return model.Run{}, false
}

func (s *Service) latestApplicableRunInWorktree(worktree, projectID, taskID string) (model.Run, bool, error) {
	root := filepath.Join(worktree, filepath.FromSlash(s.projectPrefix(projectID)+"/runs"))
	var latest model.Run
	found := false
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "run.json" {
			return nil
		}
		data, err := fsutil.ReadFileBounded(path, s.Config.MaxReadBytes)
		if err != nil {
			return err
		}
		run, _, err := model.DecodeRunRecord(data)
		if err != nil {
			return fmt.Errorf("decode run %s: %w", path, err)
		}
		if run.TaskID != taskID || run.Historical {
			return nil
		}
		if !found || run.CreatedAt.After(latest.CreatedAt) || (run.CreatedAt.Equal(latest.CreatedAt) && run.ID > latest.ID) {
			latest = run
			found = true
		}
		return nil
	})
	return latest, found, err
}

func (s *Service) findRun(ctx context.Context, id string) (model.Run, error) {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return model.Run{}, err
	}
	for _, p := range projects {
		data, err := s.Hub.ReadFile(ctx, s.runPath(p.ID, id))
		if err == nil {
			r, _, decodeErr := model.DecodeRunRecord(data)
			if decodeErr != nil {
				return model.Run{}, decodeErr
			}
			return r, nil
		}
		if !IsNotFound(err) {
			return model.Run{}, err
		}
	}
	return model.Run{}, fmt.Errorf("run not found: %s", id)
}

func (s *Service) RunRead(ctx context.Context, id string) (model.Run, error) {
	return s.findRun(ctx, id)
}

func (s *Service) RunAgentTail(ctx context.Context, id string, lines int) (string, error) {
	run, err := s.findRun(ctx, id)
	if err != nil {
		return "", err
	}
	if err := requireCanonicalRun(run); err != nil {
		return "", err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return "", err
	}
	if !operationalActiveRun(run) {
		return "", fmt.Errorf("run is not active")
	}
	if lines == 0 {
		lines = 4
	}
	if lines < 1 || lines > 200 {
		return "", fmt.Errorf("invalid tail line count: must be between 1 and 200")
	}
	result, err := s.Airelay.Tail(ctx, run.SessionKey, lines)
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func ensureOperationalRun(run model.Run) error {
	if run.Historical {
		return fmt.Errorf("workflow-v1 run is history-only")
	}
	return nil
}

func requireCanonicalTaskID(id string) error {
	if err := model.ValidateCanonicalTaskID(id); err != nil {
		return fmt.Errorf("task %q is read-only: canonical task ID required", id)
	}
	return nil
}

func requireCanonicalRunID(id string) error {
	if model.ValidateCanonicalRunID(id) != nil && model.ValidateTaskRevisionRunID(id) != nil {
		return fmt.Errorf("run %q is read-only: canonical run ID required", id)
	}
	return nil
}

func requireCanonicalRun(run model.Run) error {
	if run.TaskRevision != 0 {
		if err := model.ValidateTaskRevisionRunID(run.ID); err != nil {
			return fmt.Errorf("run %q is read-only: revision-aware run ID required", run.ID)
		}
	} else if err := requireCanonicalRunID(run.ID); err != nil {
		return err
	}
	return ensureOperationalRun(run)
}

func (s *Service) ensureRunOwned(run model.Run) error {
	if run.GatewayID != s.Config.GatewayID {
		return fmt.Errorf("run %s is assigned to gateway %s, current gateway is %s", run.ID, run.GatewayID, s.Config.GatewayID)
	}
	return nil
}

func activeStatus(s string) bool {
	switch s {
	case "created", "dispatching", "dispatched", "awaiting_result", "cancel_requested":
		return true
	}
	return false
}

// operationalActiveRun distinguishes current workflow-v2 activity from
// immutable workflow-v1 history. Historical records retain their original
// status for auditability and must never own a session or block lifecycle
// mutations.
