package mcp

import (
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (f *canonicalOutputFixture) populateCanonicalExecution() {
	now := f.now
	project := f.project.(model.Project)
	policy := f.policy.(model.ProjectWorkflowPolicy)
	task := f.task.(model.Task)
	plan := f.plan.(model.Plan)
	run := model.Run{SchemaVersion: 1, ID: "run", TaskID: "task", TaskSHA256: task.SHA256, ProjectID: "project", GatewayID: "home_pc", SessionKey: "project_master", Branch: task.Branch, BaseRevision: task.BaseRevision, HubRevision: strings.Repeat("d", 40), Status: "awaiting_result", CompletionPath: "/tmp/completion", CreatedAt: now}
	transaction := hub.TransactionResult{Before: strings.Repeat("d", 40), After: strings.Repeat("e", 40), Remote: "origin", Branch: "gpt-tunnel/home_pc", Paths: []string{"gpt-tunnel/v1/test.json"}}
	operation := service.OperationResult{Hub: transaction, ProjectID: "project", TaskID: "task", RunID: "run", Status: "updated"}
	local := config.ProjectConfig{Root: "/tmp/project", Mirror: "/tmp/mirror.git", Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "project_master"}
	worktree := gitx.WorktreeStatus{Branch: "main", Head: strings.Repeat("f", 40), Ahead: 0, Behind: 0, Porcelain: "", Clean: true}
	report := model.Report{SchemaVersion: 1, TaskID: "task", RunID: "run", ProjectID: "project", Status: "succeeded", Summary: "done", GateResults: []model.CompletionGateResult{}, AcceptanceCoverage: []string{}, Deviations: []string{}, RemainingRisks: []string{}, Repository: model.RepositoryProof{Branch: "feature/x", Head: worktree.Head, WorktreeClean: true, BaseAncestor: true, Commits: []string{}, ChangedFiles: []string{}, DiffScope: "base..head"}, FinishedAt: now}
	packet := service.TaskPacket{Task: task, Run: run, RunSummaries: []model.RunReviewSummary{}, Project: project, Plan: plan, WorkflowPolicy: policy, RepositoryRoot: "/tmp/project", CompletionPath: run.CompletionPath, FinalizeCommand: "gpt-tunnel run finalize run", Text: "packet"}
	publicRun := service.PublicRunView(run)
	publicPacket := service.PublicTaskPacketView(packet)
	sessionID := "project_master"
	operatorEvent := model.OperatorJournalEvent{SchemaVersion: 1, ID: "GTW-OPR1", ProjectID: "project", SessionID: &sessionID, Kind: model.OperatorUserTalk, Summary: "operator context", Content: model.OperatorJournalContent{Facts: []string{"fact"}}, References: model.OperatorJournalReferences{}, Actor: "owner", OccurredAt: now, RecordedAt: now}
	operatorHistory := service.OperatorHistoryResult{ProjectID: "project", Events: []model.OperatorJournalEvent{operatorEvent}, HubRevision: transaction.After, HasMore: false}
	f.run = run
	f.transaction = transaction
	f.operation = operation
	f.local = local
	f.worktree = worktree
	f.report = report
	f.packet = packet
	f.publicRun = publicRun
	f.publicPacket = publicPacket
	f.sessionID = sessionID
	f.operatorEvent = operatorEvent
	f.operatorHistory = operatorHistory
}
