package service

type StateIssue struct {
	Code      string `json:"code"`
	ProjectID string `json:"project_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	Path      string `json:"path,omitempty"`
	Detail    string `json:"detail"`
}

type StatePlan struct {
	ProjectID    string `json:"project_id"`
	Valid        bool   `json:"valid"`
	ActiveTaskID string `json:"active_task_id,omitempty"`
}

type StateCheckResult struct {
	Valid                   bool         `json:"valid"`
	HubRevision             string       `json:"hub_revision,omitempty"`
	ConfiguredProjectIDs    []string     `json:"configured_project_ids"`
	DurableProjectIDs       []string     `json:"durable_project_ids"`
	ValidCurrentPlans       []string     `json:"valid_current_plans"`
	OperationalTaskRunGraph bool         `json:"operational_task_run_graph"`
	Plans                   []StatePlan  `json:"plans"`
	Issues                  []StateIssue `json:"issues"`
}

type StateRepairAction struct {
	Kind        string `json:"kind"`
	ProjectID   string `json:"project_id"`
	TaskID      string `json:"task_id,omitempty"`
	Path        string `json:"path"`
	ClearTaskID bool   `json:"clear_active_task_id"`
	OldTaskID   string `json:"old_active_task_id,omitempty"`
	OldStatus   string `json:"old_task_status,omitempty"`
	NewStatus   string `json:"new_task_status,omitempty"`
	Reason      string `json:"reason"`
}

type StateRepairResult struct {
	DryRun       bool                `json:"dry_run"`
	Applied      bool                `json:"applied"`
	OldHubSHA    string              `json:"old_hub_sha,omitempty"`
	NewHubSHA    string              `json:"new_hub_sha,omitempty"`
	Backup       string              `json:"backup,omitempty"`
	ChangedPaths []string            `json:"changed_paths,omitempty"`
	Actions      []StateRepairAction `json:"actions"`
	Check        StateCheckResult    `json:"check"`
}

const historyOnlyTaskRepairReason = "close mutable dispatched state after linked run became immutable workflow-v1 history during protocol cutover"

const stalePlanPointerRepairReason = "clear stale active plan pointers after task or run left the operational lifecycle"

func stateIssue(code, project, task, path, detail string) StateIssue {
	return StateIssue{
		Code:      code,
		ProjectID: project,
		TaskID:    task,
		Path:      path,
		Detail:    detail,
	}
}
