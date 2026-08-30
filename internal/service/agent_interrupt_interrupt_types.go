package service

import (
	"time"
)

type AgentInterruptInput struct {
	OperationID string `json:"operation_id"`
	ProjectID   string `json:"project_id"`
	SessionKey  string `json:"session_key,omitempty"`
	AgentID     string `json:"agent_id"`
	Message     string `json:"message,omitempty"`
}
type AgentInterruptResult struct {
	OperationID      string    `json:"operation_id"`
	ProjectID        string    `json:"project_id"`
	AgentID          string    `json:"agent_id"`
	Outcome          string    `json:"outcome"`
	InterruptOutcome string    `json:"interrupt_outcome,omitempty"`
	PromptOutcome    string    `json:"prompt_outcome,omitempty"`
	Requested        bool      `json:"requested"`
	PromptDelivered  bool      `json:"prompt_delivered,omitempty"`
	ElapsedMS        int       `json:"elapsed_ms,omitempty"`
	Error            string    `json:"error,omitempty"`
	Reason           string    `json:"reason,omitempty"`
	StartedAt        time.Time `json:"started_at"`
	FinishedAt       time.Time `json:"finished_at"`
}
type agentInterruptReceipt struct {
	RequestSHA256 string               `json:"request_sha256"`
	Phase         string               `json:"phase"`
	Result        AgentInterruptResult `json:"result"`
}

const agentInterruptReceiptLimit = 1 << 20
