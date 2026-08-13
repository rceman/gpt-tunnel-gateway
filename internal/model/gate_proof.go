package model

import "time"

type CompletionGateResult struct {
	ID             string `json:"id"`
	ExitCode       int    `json:"exit_code"`
	Execution      string `json:"execution,omitempty"`
	TreeID         string `json:"tree_id,omitempty"`
	ContractDigest string `json:"contract_digest,omitempty"`
	ReceiptDigest  string `json:"receipt_digest,omitempty"`
}

type RepositoryProof struct {
	Branch        string   `json:"branch"`
	Head          string   `json:"head"`
	WorktreeClean bool     `json:"worktree_clean"`
	BaseAncestor  bool     `json:"base_ancestor"`
	Commits       []string `json:"commits"`
	ChangedFiles  []string `json:"changed_files"`
	DiffScope     string   `json:"diff_scope"`
}

type AttemptProofSummary struct {
	CheckpointHead    string                 `json:"checkpoint_head,omitempty"`
	ImplementationSHA string                 `json:"implementation_sha,omitempty"`
	GateResults       []CompletionGateResult `json:"gate_results"`
	RecordedAt        time.Time              `json:"recorded_at"`
}
