package service

import (
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// PublicRun is the run projection used at MCP/CLI boundaries.  The persisted
// run still retains its internally resolved session key, but public callers
// never receive it.
type PublicRun struct {
	SchemaVersion    int        `json:"schema_version"`
	ID               string     `json:"id"`
	TaskID           string     `json:"task_id"`
	TaskSHA256       string     `json:"task_sha256"`
	ProjectID        string     `json:"project_id"`
	GatewayID        string     `json:"gateway_id"`
	Branch           string     `json:"branch"`
	BaseRevision     string     `json:"base_revision"`
	HubRevision      string     `json:"hub_revision"`
	Status           string     `json:"status"`
	DispatchMessage  string     `json:"dispatch_message,omitempty"`
	DispatchExitCode *int       `json:"dispatch_exit_code,omitempty"`
	DispatchStdout   string     `json:"dispatch_stdout,omitempty"`
	DispatchStderr   string     `json:"dispatch_stderr,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	DispatchedAt     *time.Time `json:"dispatched_at,omitempty"`
	RepromptCount    int        `json:"reprompt_count,omitempty"`
	LastRepromptAt   *time.Time `json:"last_reprompt_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
}

func PublicRunView(run model.Run) PublicRun {
	return PublicRun{
		SchemaVersion: run.SchemaVersion, ID: run.ID, TaskID: run.TaskID, TaskSHA256: run.TaskSHA256,
		ProjectID: run.ProjectID, GatewayID: run.GatewayID, Branch: run.Branch, BaseRevision: run.BaseRevision,
		HubRevision: run.HubRevision, Status: run.Status, DispatchMessage: run.DispatchMessage,
		DispatchExitCode: run.DispatchExitCode, DispatchStdout: run.DispatchStdout, DispatchStderr: run.DispatchStderr,
		CreatedAt: run.CreatedAt, DispatchedAt: run.DispatchedAt,
		RepromptCount: run.RepromptCount, LastRepromptAt: run.LastRepromptAt, FinishedAt: run.FinishedAt,
	}
}

type PublicTaskPacketRun struct {
	PublicRun
	CompletionPath string `json:"completion_path"`
}

type PublicTaskPacket struct {
	Task            model.Task          `json:"task"`
	Run             PublicTaskPacketRun `json:"run"`
	Project         model.Project       `json:"project"`
	Plan            model.Plan          `json:"plan"`
	RepositoryRoot  string              `json:"repository_root"`
	CompletionPath  string              `json:"completion_path"`
	FinalizeCommand string              `json:"finalize_command"`
	Text            string              `json:"text"`
}

func PublicTaskPacketView(packet TaskPacket) PublicTaskPacket {
	return PublicTaskPacket{Task: packet.Task, Run: PublicTaskPacketRun{PublicRun: PublicRunView(packet.Run), CompletionPath: packet.Run.CompletionPath}, Project: packet.Project, Plan: packet.Plan, RepositoryRoot: packet.RepositoryRoot, CompletionPath: packet.CompletionPath, FinalizeCommand: packet.FinalizeCommand, Text: packet.Text}
}
