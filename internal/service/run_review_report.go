package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
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
	task       model.Task
	run        model.Run
	agent      model.Report
	branch     string
	head       string
	clean      bool
	repository model.ReviewRepositoryState
	gates      []model.CompletionGateResult
	changed    []string
}

func sameAgentAuthority(left, right model.Report) bool {
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
		task:       task,
		run:        run,
		agent:      agent,
		branch:     branch,
		head:       head,
		clean:      clean,
		repository: model.ReviewRepositoryState{Branch: branch, BaseRevision: run.BaseRevision, ReviewedHead: head, WorktreeClean: clean, BaseAncestor: agent.Repository.BaseAncestor},
		gates:      append([]model.CompletionGateResult{}, agent.GateResults...),
		changed:    changed,
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

func (s *Service) TaskReviewReportStart(ctx context.Context, taskID, runID string) (model.RunReviewReportDraft, error) {
	lock, err := s.reviewReportLock(runID)
	if err != nil {
		return model.RunReviewReportDraft{}, err
	}
	defer lock.Release()
	context, err := s.loadReviewContext(ctx, taskID, runID)
	if err != nil {
		return model.RunReviewReportDraft{}, err
	}
	if exists, err := s.reviewReportExists(ctx, context.task.ProjectID, runID); err != nil {
		return model.RunReviewReportDraft{}, err
	} else if exists {
		return model.RunReviewReportDraft{}, fmt.Errorf("delivery review report already finalized")
	}
	if draft, err := s.readReviewDraft(runID); err == nil {
		if draft.TaskID != taskID || draft.RunID != runID || draft.ProjectID != context.task.ProjectID {
			return model.RunReviewReportDraft{}, fmt.Errorf("review draft identity mismatch")
		}
		s.reviewMachineDraft(context, &draft)
		if err := s.writeReviewDraft(draft); err != nil {
			return model.RunReviewReportDraft{}, err
		}
		return draft, nil
	} else if !os.IsNotExist(err) {
		return model.RunReviewReportDraft{}, err
	}
	draft := model.RunReviewReportDraft{DraftRevision: 1, UpdatedAt: time.Now().UTC(), CompletedSections: []string{"repository_state", "gates", "changed_files"}}
	s.reviewMachineDraft(context, &draft)
	if err := s.writeReviewDraft(draft); err != nil {
		return model.RunReviewReportDraft{}, err
	}
	return draft, nil
}

func decodeReviewPayload(data []byte, out any) error {
	if len(data) == 0 {
		return fmt.Errorf("review section payload is required")
	}
	return decodeStrict(data, out)
}

func (s *Service) TaskReviewReportSectionUpdate(ctx context.Context, in TaskReviewReportSectionUpdateInput) (model.RunReviewReportDraft, error) {
	lock, err := s.reviewReportLock(in.RunID)
	if err != nil {
		return model.RunReviewReportDraft{}, err
	}
	defer lock.Release()
	context, err := s.loadReviewContext(ctx, in.TaskID, in.RunID)
	if err != nil {
		return model.RunReviewReportDraft{}, err
	}
	draft, err := s.readReviewDraft(in.RunID)
	if err != nil {
		return model.RunReviewReportDraft{}, err
	}
	if draft.TaskID != in.TaskID || draft.RunID != in.RunID || draft.ProjectID != context.task.ProjectID {
		return model.RunReviewReportDraft{}, fmt.Errorf("review draft identity mismatch")
	}
	if in.ExpectedDraftRevision != draft.DraftRevision {
		return model.RunReviewReportDraft{}, fmt.Errorf("DRAFT_REVISION_CONFLICT expected=%d actual=%d", in.ExpectedDraftRevision, draft.DraftRevision)
	}
	switch in.SectionID {
	case "repository_state", "gates", "changed_files":
		return model.RunReviewReportDraft{}, fmt.Errorf("machine review section %q cannot be edited", in.SectionID)
	case "outcome":
		if err := decodeReviewPayload(in.Payload, &draft.Outcome); err != nil {
			return model.RunReviewReportDraft{}, err
		}
	case "findings":
		if err := decodeReviewPayload(in.Payload, &draft.Findings); err != nil {
			return model.RunReviewReportDraft{}, err
		}
	case "scope_coverage":
		if err := decodeReviewPayload(in.Payload, &draft.ScopeCoverage); err != nil {
			return model.RunReviewReportDraft{}, err
		}
	case "unexpected_surfaces":
		if err := decodeReviewPayload(in.Payload, &draft.UnexpectedSurfaces); err != nil {
			return model.RunReviewReportDraft{}, err
		}
	case "historical_compatibility":
		if err := decodeReviewPayload(in.Payload, &draft.HistoricalCompatibility); err != nil {
			return model.RunReviewReportDraft{}, err
		}
	case "prohibited_actions":
		if err := decodeReviewPayload(in.Payload, &draft.ProhibitedActions); err != nil {
			return model.RunReviewReportDraft{}, err
		}
	case "next_action":
		if err := decodeReviewPayload(in.Payload, &draft.NextAction); err != nil {
			return model.RunReviewReportDraft{}, err
		}
	default:
		return model.RunReviewReportDraft{}, fmt.Errorf("unknown review section %q", in.SectionID)
	}
	s.reviewMachineDraft(context, &draft)
	draft.CompletedSections = addReviewSection(draft.CompletedSections, in.SectionID)
	draft.DraftRevision++
	draft.UpdatedAt = time.Now().UTC()
	if err := s.writeReviewDraft(draft); err != nil {
		return model.RunReviewReportDraft{}, err
	}
	return draft, nil
}

func reviewDraftMissingSections(draft model.RunReviewReportDraft) []string {
	missing := []string{}
	for _, section := range model.RunReviewReportSections {
		if !reviewSectionSeen(draft.CompletedSections, section) {
			missing = append(missing, section)
		}
	}
	if draft.Outcome == "" {
		missing = append(missing, "outcome_value")
	}
	if strings.TrimSpace(draft.NextAction) == "" {
		missing = append(missing, "next_action_value")
	}
	return missing
}

func (s *Service) TaskReviewReportValidate(ctx context.Context, taskID, runID string) (model.RunReviewValidation, error) {
	lock, err := s.reviewReportLock(runID)
	if err != nil {
		return model.RunReviewValidation{}, err
	}
	defer lock.Release()
	context, err := s.loadReviewContext(ctx, taskID, runID)
	if err != nil {
		return model.RunReviewValidation{}, err
	}
	draft, err := s.readReviewDraft(runID)
	if err != nil {
		return model.RunReviewValidation{}, err
	}
	s.reviewMachineDraft(context, &draft)
	result := model.RunReviewValidation{Valid: true, Errors: []string{}, Draft: draft}
	if err := model.ValidateRunReviewReportDraft(draft); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, err.Error())
	}
	result.Errors = append(result.Errors, reviewDraftMissingSections(draft)...)
	if len(result.Errors) > 0 {
		result.Valid = false
	}
	if err := s.writeReviewDraft(draft); err != nil {
		return model.RunReviewValidation{}, err
	}
	return result, nil
}

func (s *Service) TaskReviewReportFinalize(ctx context.Context, in TaskReviewReportFinalizeInput) (model.RunReviewReport, OperationResult, error) {
	lock, err := s.reviewReportLock(in.RunID)
	if err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	defer lock.Release()
	context, err := s.loadReviewContext(ctx, in.TaskID, in.RunID)
	if err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	draft, err := s.readReviewDraft(in.RunID)
	if err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	if in.ExpectedDraftRevision != draft.DraftRevision {
		return model.RunReviewReport{}, OperationResult{}, fmt.Errorf("DRAFT_REVISION_CONFLICT expected=%d actual=%d", in.ExpectedDraftRevision, draft.DraftRevision)
	}
	s.reviewMachineDraft(context, &draft)
	if missing := reviewDraftMissingSections(draft); len(missing) > 0 {
		return model.RunReviewReport{}, OperationResult{}, fmt.Errorf("review draft incomplete: %s", strings.Join(missing, ", "))
	}
	report := model.RunReviewReport{
		SchemaVersion:           model.RunReviewReportSchemaVersion,
		ID:                      model.NewRunReviewReportID(context.run.ID),
		TaskID:                  context.task.ID,
		RunID:                   context.run.ID,
		ProjectID:               context.task.ProjectID,
		TaskSHA256:              context.task.SHA256,
		TaskRevision:            context.run.TaskRevision,
		TaskRevisionSHA256:      context.run.TaskRevisionSHA256,
		TaskRunNumber:           context.run.TaskRunNumber,
		Branch:                  context.branch,
		BaseRevision:            context.run.BaseRevision,
		ReviewedHead:            context.head,
		Outcome:                 draft.Outcome,
		RepositoryState:         context.repository,
		Gates:                   append([]model.CompletionGateResult{}, context.gates...),
		Findings:                append([]model.ReviewFinding{}, draft.Findings...),
		ScopeCoverage:           append([]model.ReviewScopeCoverage{}, draft.ScopeCoverage...),
		ChangedFiles:            append([]string{}, context.changed...),
		UnexpectedSurfaces:      append([]string{}, draft.UnexpectedSurfaces...),
		HistoricalCompatibility: append([]string{}, draft.HistoricalCompatibility...),
		ProhibitedActions:       append([]string{}, draft.ProhibitedActions...),
		NextAction:              draft.NextAction,
		FinishedAt:              time.Now().UTC(),
	}
	if err := model.ValidateRunReviewReport(report); err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return model.RunReviewReport{}, OperationResult{}, err
		}
	}
	path := s.reviewReportPath(context.task.ProjectID, context.run.ID)
	tx, err := s.Hub.Transact(ctx, expected, "gateway: finalize delivery review "+report.ID, func(worktree string) ([]string, error) {
		var currentTask model.Task
		if err := readWorktreeJSON(worktree, s.taskPath(context.task.ProjectID, context.task.ID), &currentTask); err != nil {
			return nil, err
		}
		if err := model.ValidateTask(currentTask); err != nil || currentTask.ID != context.task.ID || currentTask.SHA256 != context.task.SHA256 {
			return nil, fmt.Errorf("task changed before delivery review publication")
		}
		if err := model.ValidateTaskHash(currentTask); err != nil || currentTask.SHA256 != context.task.SHA256 {
			return nil, fmt.Errorf("task hash changed before delivery review publication")
		}
		var currentRun model.Run
		if err := readWorktreeJSON(worktree, s.runPath(context.task.ProjectID, context.run.ID), &currentRun); err != nil {
			return nil, err
		}
		if err := model.ValidateRun(currentRun); err != nil || currentRun.Historical || currentRun.ID != context.run.ID || currentRun.TaskID != context.task.ID || currentRun.TaskSHA256 != context.task.SHA256 || currentRun.TaskRevision != context.run.TaskRevision || currentRun.TaskRevisionSHA256 != context.run.TaskRevisionSHA256 || currentRun.TaskRunNumber != context.run.TaskRunNumber || currentRun.ProjectID != context.task.ProjectID || currentRun.Branch != context.run.Branch || currentRun.BaseRevision != context.run.BaseRevision || operationalActiveRun(currentRun) {
			return nil, fmt.Errorf("run changed or is still operational before delivery review publication")
		}
		currentAgent, err := s.readWorktreeReport(worktree, context.run, context.task)
		if err != nil {
			return nil, err
		}
		if err := model.ValidateReport(currentAgent, currentTask, currentRun, s.Config.MaxListItems); err != nil {
			return nil, fmt.Errorf("Agent report changed before delivery review publication: %w", err)
		}
		if currentAgent.TaskID != context.task.ID || currentAgent.RunID != context.run.ID || currentAgent.TaskRevision != context.run.TaskRevision || currentAgent.TaskRevisionSHA256 != context.run.TaskRevisionSHA256 || currentAgent.TaskRunNumber != context.run.TaskRunNumber || currentAgent.ProjectID != context.task.ProjectID || currentAgent.Repository.Head != context.head || currentAgent.Repository.Branch != context.branch || currentAgent.Repository.DiffScope != context.run.BaseRevision+".."+context.head || currentAgent.Status != currentRun.Status || !sameAgentAuthority(currentAgent, context.agent) {
			return nil, fmt.Errorf("Agent report changed before delivery review publication")
		}
		if _, err := os.Lstat(filepath.Join(worktree, filepath.FromSlash(path))); err == nil {
			return nil, fmt.Errorf("delivery review report already finalized")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, path, report); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		return model.RunReviewReport{}, OperationResult{}, err
	}
	report.HubCommit = tx.After
	_ = os.Remove(s.reviewReportDraftPath(in.RunID))
	return report, OperationResult{Hub: tx, ProjectID: context.task.ProjectID, TaskID: context.task.ID, RunID: context.run.ID, Status: "review_report_finalized"}, nil
}

func (s *Service) readFinalReviewReport(ctx context.Context, task model.Task, run model.Run) (model.RunReviewReport, error) {
	data, err := s.Hub.ReadFile(ctx, s.reviewReportPath(run.ProjectID, run.ID))
	if err != nil {
		return model.RunReviewReport{}, err
	}
	report, err := model.ParseRunReviewReport(data)
	if err != nil {
		return model.RunReviewReport{}, err
	}
	if report.TaskID != task.ID || report.RunID != run.ID || report.ProjectID != task.ProjectID || report.TaskSHA256 != task.SHA256 || report.TaskRevision != run.TaskRevision || report.TaskRevisionSHA256 != run.TaskRevisionSHA256 || report.TaskRunNumber != run.TaskRunNumber || report.Branch != run.Branch || report.BaseRevision != run.BaseRevision {
		return model.RunReviewReport{}, fmt.Errorf("delivery review report identity mismatch")
	}
	if report.HubCommit == "" {
		report.HubCommit, _ = s.Hub.LastChange(ctx, s.reviewReportPath(run.ProjectID, run.ID))
	}
	return report, nil
}

func (s *Service) TaskReportRead(ctx context.Context, taskID, runID string) (model.RunReviewReport, error) {
	task, err := s.findTask(ctx, taskID)
	if err != nil {
		return model.RunReviewReport{}, err
	}
	runs, err := s.RunList(ctx, task.ProjectID)
	if err != nil {
		return model.RunReviewReport{}, err
	}
	if runID != "" {
		for _, run := range runs {
			if run.ID != runID {
				continue
			}
			if run.TaskID != task.ID {
				return model.RunReviewReport{}, fmt.Errorf("run does not belong to task")
			}
			return s.readFinalReviewReport(ctx, task, run)
		}
		return model.RunReviewReport{}, fmt.Errorf("run not found for task")
	}
	revision := 0
	var revisionSHA string
	if model.ValidateCanonicalTaskID(task.ID) == nil {
		current, revisionErr := s.currentTaskRevision(ctx, task)
		if revisionErr != nil {
			return model.RunReviewReport{}, revisionErr
		}
		revision = current.TaskRevision
		revisionSHA = current.RevisionSHA256
	}
	latest, ok := latestApplicableRunForRevision(runs, task.ID, revision, revisionSHA)
	if !ok {
		return model.RunReviewReport{}, fmt.Errorf("no applicable run for task %s", task.ID)
	}
	report, err := s.readFinalReviewReport(ctx, task, latest)
	if err == nil {
		return report, nil
	}
	if IsNotFound(err) {
		return model.RunReviewReport{}, fmt.Errorf("latest applicable run %s is awaiting Delivery review", latest.ID)
	}
	return model.RunReviewReport{}, err
}

func (s *Service) taskReviewSummaries(ctx context.Context, task model.Task, runs []model.Run) ([]model.RunReviewSummary, error) {
	items := make([]model.RunReviewSummary, 0)
	for _, run := range runs {
		if run.TaskID != task.ID {
			continue
		}
		item := model.RunReviewSummary{RunID: run.ID, AgentStatus: run.Status, DeliveryStatus: "not_available", HistoryOnly: run.Historical}
		if run.Historical {
			item.DeliveryStatus = "history_only"
			items = append(items, item)
			continue
		}
		report, err := s.readFinalReviewReport(ctx, task, run)
		if err == nil {
			item.DeliveryStatus = "finalized"
			item.DeliveryReportID = report.ID
			item.DeliveryOutcome = report.Outcome
			item.ReviewedHead = report.ReviewedHead
			item.NextAction = report.NextAction
			if report.Outcome != model.ReviewOutcomeAccepted {
				item.Blocker = report.Outcome
			}
		} else if IsNotFound(err) {
			if run.Status == "succeeded" {
				item.DeliveryStatus = "awaiting_review"
				item.Blocker = "awaiting_review"
				item.NextAction = "perform_delivery_review"
			}
		} else {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}
