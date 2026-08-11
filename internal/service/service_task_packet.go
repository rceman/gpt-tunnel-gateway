package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) TaskRead(ctx context.Context, id string) (TaskPacket, error) {
	task, err := s.findTask(ctx, id)
	if err != nil {
		return TaskPacket{}, err
	}
	var currentRevision *model.TaskRevision
	if model.ValidateCanonicalTaskID(task.ID) == nil {
		revision, revisionErr := s.currentTaskRevision(ctx, task)
		if revisionErr != nil {
			return TaskPacket{}, revisionErr
		}
		currentRevision = &revision
	}
	runs, err := s.RunList(ctx, task.ProjectID)
	if err != nil {
		return TaskPacket{}, err
	}
	matches := []model.Run{}
	for _, r := range runs {
		canonicalRun := model.ValidateCanonicalRunID(r.ID) == nil
		if r.TaskRevision != 0 {
			canonicalRun = model.ValidateTaskRevisionRunID(r.ID) == nil
		}
		if r.TaskID == task.ID && operationalActiveRun(r) && canonicalRun {
			matches = append(matches, r)
		}
	}
	if len(matches) != 1 {
		return TaskPacket{}, fmt.Errorf("expected exactly one active run for task, found %d", len(matches))
	}
	run := matches[0]
	if err := requireCanonicalRun(run); err != nil {
		return TaskPacket{}, err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return TaskPacket{}, err
	}
	project, err := s.ProjectRead(ctx, task.ProjectID)
	if err != nil {
		return TaskPacket{}, err
	}
	plan, err := s.PlanRead(ctx, task.ProjectID)
	if err != nil {
		return TaskPacket{}, err
	}
	local, err := s.projectConfig(task.ProjectID)
	if err != nil {
		return TaskPacket{}, err
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, task.ProjectID)
	if err != nil {
		return TaskPacket{}, fmt.Errorf("read task workflow policy: %w", err)
	}
	text := renderPacket(task, run, project, plan, policy, local.Root)
	summaries, err := s.taskReviewSummaries(ctx, task, runs)
	if err != nil {
		return TaskPacket{}, err
	}
	return TaskPacket{
		Task:            task,
		CurrentRevision: currentRevision,
		Run:             run,
		RunSummaries:    summaries,
		Project:         project,
		Plan:            plan,
		WorkflowPolicy:  policy,
		RepositoryRoot:  local.Root,
		CompletionPath:  run.CompletionPath,
		FinalizeCommand: "gpt-tunnel run finalize " + run.ID + " --summary <text>",
		Text:            text,
	}, nil
}

func renderPacket(task model.Task, run model.Run, project model.Project, plan model.Plan, policy model.ProjectWorkflowPolicy, root string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# GPT Tunnel Agent Execution Packet\n\nTask: %s\nRun: %s\nProject: %s\nRepository: %s\nBranch: %s\nBase: %s\n\n## Objective\n\n%s\n\n## Acceptance criteria\n", task.ID, run.ID, project.ID, root, run.Branch, run.BaseRevision, task.Objective)
	for _, v := range task.AcceptanceCriteria {
		fmt.Fprintf(&b, "- %s\n", v)
	}
	b.WriteString("\n## Constraints\n")
	for _, v := range task.Constraints {
		fmt.Fprintf(&b, "- %s\n", v)
	}
	b.WriteString("\n## Required gates\n")
	for _, v := range task.RequiredGates {
		fmt.Fprintf(&b, "- %s\n", v)
	}
	fmt.Fprintf(&b, "\n## Durable workflow policy\n\nWorkflow stage: %s\nIntegration branch authority: %s\nAgent wait for hosted CI: %t\nCI modes: task=%s, task_merge=%s, release=%s\nEffective operation class: %s\nEffective CI field/mode: %s/%s\nEffective wait_for_ci: %t\nEffective ci_blocking: %t\nAgent may wait: %t\n\nCurrent Gateway implementation, integration and release tasks do not wait for hosted CI unless the durable project policy explicitly requires it.\n\n## Global plan context\n\n%s\n\nCurrent objective: %s\n\n## Context-compaction recovery\n\nIf context is lost or a compaction marker appears, re-read this immutable task packet with `gpt-tunnel task read %s`. Inspect the declared branch, base, current HEAD, worktree, existing commits, and durable run state. Resume from committed and durable evidence; do not rely on conversation memory, redo completed phases, or change task scope. If implementation is already complete, continue through verification, completion evidence, push, and finalization.\n\n## Completion contract\n\nCommit the implementation, run every required gate, and push the task branch. Then finalize directly with the bounded Agent-owned summary:\n  gpt-tunnel run finalize %s --summary <text>\n\nThe Gateway derives Task/Run identity, revision, repository proof, gate evidence, completion destination, and terminal report from durable state. Do not create completion.json or provide task, revision, repository, or gate metadata. The task is not complete until finalization prints TASK_FINALIZED; if blocked, report the explicit blocker.\n\nTo read a prior Delivery report, use exactly `gpt-tunnel task report-read <TASK-ID> [RUN-ID]`.\n", policy.WorkflowStage, policy.IntegrationBranch, policy.Agent.WaitForCI, policy.CI.Task, policy.CI.TaskMerge, policy.CI.Release, task.OperationClass, task.EffectiveCIField, task.EffectiveCIMode, task.WaitForCI, task.CIBlocking, task.AgentMayWait, plan.Summary, plan.CurrentObjective, task.ID, run.ID)
	return b.String()
}

func normalizedAbsolutePath(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func gatewayCompletionPath(run model.Run, requested string) (string, error) {
	if run.CompletionPath == "" {
		return "", fmt.Errorf("run has no gateway-owned completion path")
	}
	expected, err := normalizedAbsolutePath(run.CompletionPath)
	if err != nil {
		return "", fmt.Errorf("invalid gateway completion path")
	}
	if requested == "" {
		requested = run.CompletionPath
	}
	actual, err := normalizedAbsolutePath(requested)
	if err != nil || actual != expected {
		return "", fmt.Errorf("completion file must equal the gateway-owned run completion path")
	}
	info, err := os.Lstat(actual)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("completion file is not a regular gateway-owned file")
	}
	resolved, err := filepath.EvalSymlinks(actual)
	if err != nil {
		return "", err
	}
	resolved, err = normalizedAbsolutePath(resolved)
	if err != nil || resolved != actual {
		return "", fmt.Errorf("completion file must not resolve outside the gateway-owned path")
	}
	return actual, nil
}

func canonicalCompletionDestination(stateDir, runID string) (string, error) {
	if err := requireCanonicalRunID(runID); err != nil {
		return "", err
	}
	stateRoot, err := normalizedAbsolutePath(stateDir)
	if err != nil {
		return "", fmt.Errorf("invalid gateway state directory")
	}
	return filepath.Join(stateRoot, "runs", runID, "completion.json"), nil
}
