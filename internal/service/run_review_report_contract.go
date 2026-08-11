package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type TaskReviewReportSectionUpdateInput struct {
	TaskID                string          `json:"task_id"`
	RunID                 string          `json:"run_id"`
	SectionID             string          `json:"section_id"`
	ExpectedDraftRevision int             `json:"expected_draft_revision"`
	Payload               json.RawMessage `json:"payload"`
}

type TaskReviewReportFinalizeInput struct {
	TaskID                string `json:"task_id"`
	RunID                 string `json:"run_id"`
	ExpectedDraftRevision int    `json:"expected_draft_revision"`
	WriteOptions
}

type reviewContext struct {
	task        model.Task
	run         model.Run
	agent       model.Report
	branch      string
	head        string
	clean       bool
	repository  model.ReviewRepositoryState
	gates       []model.CompletionGateResult
	serverGates []model.CompletionGateResult
	changed     []string
}

func sameAgentAuthority(left, right model.Report) bool {
	left = canonicalReport(left)
	right = canonicalReport(right)
	left.HubCommit = ""
	right.HubCommit = ""
	return reflect.DeepEqual(left, right)
}

func (s *Service) reviewReportPath(project, runID string) string {
	return s.runPrefix(project, runID) + "/review-report.json"
}

func (s *Service) reviewReportDraftPath(runID string) string {
	return filepath.Join(s.localRunDir(runID), "review-report-draft.json")
}

func (s *Service) reviewReportLock(runID string) (*lockfile.Lock, error) {
	if err := model.ValidateObjectIdentifier(runID); err != nil {
		return nil, err
	}
	return lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "review-report-"+runID)
}

func (s *Service) loadReviewContext(ctx context.Context, taskID, runID string) (reviewContext, error) {
	var out reviewContext
	task, err := s.findTask(ctx, taskID)
	if err != nil {
		return out, err
	}
	run, err := s.findRun(ctx, runID)
	if err != nil {
		return out, err
	}
	if run.ProjectID != task.ProjectID || run.TaskID != task.ID {
		return out, fmt.Errorf("task and run ownership mismatch")
	}
	if err := requireCanonicalRun(run); err != nil {
		return out, err
	}
	if run.Historical || operationalActiveRun(run) {
		return out, fmt.Errorf("delivery review requires a terminal operational run")
	}
	if err := model.ValidateTask(task); err != nil {
		return out, err
	}
	if err := model.ValidateTaskHash(task); err != nil || run.TaskSHA256 != task.SHA256 {
		return out, fmt.Errorf("task hash does not match run")
	}
	agent, err := s.RunReport(ctx, run.ID)
	if err != nil {
		return out, err
	}
	local, err := s.projectConfig(task.ProjectID)
	if err != nil {
		return out, err
	}
	head, branch, clean, err := s.Git.CurrentHead(ctx, local)
	if err != nil {
		return out, err
	}
	if branch != run.Branch || head != agent.Repository.Head {
		return out, fmt.Errorf("reviewed source head or branch changed since Agent finalization")
	}
	if agent.Repository.Branch != run.Branch || agent.Repository.DiffScope != run.BaseRevision+".."+head {
		return out, fmt.Errorf("Agent repository proof does not match the Run")
	}
	changed := append([]string{}, agent.Repository.ChangedFiles...)
	sort.Strings(changed)
	if !sameStrings(changed, agent.Repository.ChangedFiles) {
		return out, fmt.Errorf("Agent changed files are not canonical")
	}
	out = reviewContext{
		task:        task,
		run:         run,
		agent:       agent,
		branch:      branch,
		head:        head,
		clean:       clean,
		repository:  model.ReviewRepositoryState{Branch: branch, BaseRevision: run.BaseRevision, ReviewedHead: head, WorktreeClean: clean, BaseAncestor: agent.Repository.BaseAncestor},
		gates:       append([]model.CompletionGateResult{}, agent.GateResults...),
		serverGates: append([]model.CompletionGateResult{}, agent.ServerGateResults...),
		changed:     changed,
	}
	return out, nil
}

func (s *Service) reviewMachineDraft(ctx reviewContext, draft *model.RunReviewReportDraft) {
	draft.SchemaVersion = model.RunReviewReportSchemaVersion
	draft.ID = model.NewRunReviewReportID(ctx.run.ID)
	draft.TaskID = ctx.task.ID
	draft.RunID = ctx.run.ID
	draft.ProjectID = ctx.task.ProjectID
	draft.TaskSHA256 = ctx.task.SHA256
	draft.TaskRevision = ctx.run.TaskRevision
	draft.TaskRevisionSHA256 = ctx.run.TaskRevisionSHA256
	draft.TaskRunNumber = ctx.run.TaskRunNumber
	draft.Branch = ctx.branch
	draft.BaseRevision = ctx.run.BaseRevision
	draft.ReviewedHead = ctx.head
	draft.RepositoryState = ctx.repository
	draft.Gates = append([]model.CompletionGateResult{}, ctx.gates...)
	draft.ServerGateResults = append([]model.CompletionGateResult{}, ctx.serverGates...)
	draft.ChangedFiles = append([]string{}, ctx.changed...)
}

func (s *Service) readReviewDraft(runID string) (model.RunReviewReportDraft, error) {
	path := s.reviewReportDraftPath(runID)
	info, err := os.Lstat(path)
	if err != nil {
		return model.RunReviewReportDraft{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return model.RunReviewReportDraft{}, fmt.Errorf("review draft is not a regular file")
	}
	data, err := fsutil.ReadFileBounded(path, s.Config.MaxReadBytes)
	if err != nil {
		return model.RunReviewReportDraft{}, err
	}
	return model.ParseRunReviewReportDraft(data)
}

func (s *Service) writeReviewDraft(draft model.RunReviewReportDraft) error {
	if err := model.ValidateRunReviewReportDraft(draft); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(s.reviewReportDraftPath(draft.RunID), draft, 0o600)
}

func reviewSectionSeen(sections []string, wanted string) bool {
	for _, section := range sections {
		if section == wanted {
			return true
		}
	}
	return false
}

func addReviewSection(sections []string, section string) []string {
	if reviewSectionSeen(sections, section) {
		return sections
	}
	return append(sections, section)
}

func (s *Service) reviewReportExists(ctx context.Context, project, runID string) (bool, error) {
	_, err := s.Hub.ReadFile(ctx, s.reviewReportPath(project, runID))
	if err == nil {
		return true, nil
	}
	if IsNotFound(err) {
		return false, nil
	}
	return false, err
}
