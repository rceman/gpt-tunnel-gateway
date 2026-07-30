package model

import "time"

type ReviewSnapshot struct {
	SchemaVersion int                    `json:"schema_version"`
	Run           ReviewSnapshotRun      `json:"run"`
	Task          ReviewSnapshotTask     `json:"task"`
	Report        ReviewSnapshotReport   `json:"report"`
	Evidence      ReviewSnapshotEvidence `json:"evidence"`
	Repository    ReviewSnapshotRepo     `json:"repository"`
	Checks        []ReviewSnapshotCheck  `json:"checks"`
	ReviewState   string                 `json:"review_state"`
	NextAction    string                 `json:"next_action"`
}

type ReviewSnapshotRun struct {
	ID           string     `json:"id"`
	TaskID       string     `json:"task_id"`
	ProjectID    string     `json:"project_id"`
	Status       string     `json:"status"`
	Branch       string     `json:"branch"`
	BaseRevision string     `json:"base_revision"`
	CreatedAt    time.Time  `json:"created_at"`
	DispatchedAt *time.Time `json:"dispatched_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

type ReviewSnapshotTask struct {
	ID                 string    `json:"id"`
	SHA256             string    `json:"sha256"`
	Title              string    `json:"title"`
	Objective          string    `json:"objective"`
	Branch             string    `json:"branch"`
	BaseRevision       string    `json:"base_revision"`
	AcceptanceCriteria []string  `json:"acceptance_criteria"`
	Constraints        []string  `json:"constraints"`
	RequiredGates      []string  `json:"required_gates"`
	CreatedBy          string    `json:"created_by"`
	CreatedAt          time.Time `json:"created_at"`
	TaskStateStatus    string    `json:"task_state_status"`
}

type ReviewSnapshotReport struct {
	Available      bool            `json:"available"`
	Error          string          `json:"error,omitempty"`
	Status         string          `json:"status,omitempty"`
	Summary        string          `json:"summary,omitempty"`
	Commits        []string        `json:"commits,omitempty"`
	ChangedFiles   []string        `json:"changed_files,omitempty"`
	Commands       []CommandResult `json:"commands,omitempty"`
	Deviations     []string        `json:"deviations,omitempty"`
	RemainingRisks []string        `json:"remaining_risks,omitempty"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
	HubCommit      string          `json:"hub_commit,omitempty"`
}

type ReviewSnapshotEvidence struct {
	Available     bool       `json:"available"`
	Error         string     `json:"error,omitempty"`
	Head          string     `json:"head,omitempty"`
	Branch        string     `json:"branch,omitempty"`
	WorktreeClean *bool      `json:"worktree_clean,omitempty"`
	Notes         []string   `json:"notes,omitempty"`
	RecordedAt    *time.Time `json:"recorded_at,omitempty"`
}

type ReviewSnapshotWorktree struct {
	Branch   string `json:"branch"`
	Head     string `json:"head"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Clean    bool   `json:"clean"`
}

type ReviewSnapshotCompare struct {
	MergeBase string `json:"merge_base,omitempty"`
	LeftOnly  int    `json:"left_only"`
	RightOnly int    `json:"right_only"`
	Error     string `json:"error,omitempty"`
}

type ReviewSnapshotRepo struct {
	RefreshAttempted      bool                   `json:"refresh_attempted"`
	RefreshSucceeded      bool                   `json:"refresh_succeeded"`
	RefreshError          string                 `json:"refresh_error,omitempty"`
	DefaultBranch         string                 `json:"default_branch"`
	DefaultHead           string                 `json:"default_head,omitempty"`
	DefaultHeadError      string                 `json:"default_head_error,omitempty"`
	TaskBranch            string                 `json:"task_branch"`
	TaskBranchPublished   bool                   `json:"task_branch_published"`
	TaskBranchHead        string                 `json:"task_branch_head,omitempty"`
	TaskBranchError       string                 `json:"task_branch_error,omitempty"`
	Worktree              ReviewSnapshotWorktree `json:"worktree"`
	WorktreeError         string                 `json:"worktree_error,omitempty"`
	EvidenceHeadReachable bool                   `json:"evidence_head_reachable"`
	EvidenceHeadError     string                 `json:"evidence_head_error,omitempty"`
	BaseToEvidence        ReviewSnapshotCompare  `json:"base_to_evidence"`
	DefaultToEvidence     ReviewSnapshotCompare  `json:"default_to_evidence"`
	ChangedFilesError     string                 `json:"changed_files_error,omitempty"`
	DiffStatError         string                 `json:"diff_stat_error,omitempty"`
	ChangedFiles          []string               `json:"changed_files"`
	DiffStat              string                 `json:"diff_stat,omitempty"`
}

type ReviewSnapshotCheck struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
}
