package service

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) projectProgressFromInputs(plan model.Plan, planErr error, tasks []TaskRecord, tasksErr error, runs []model.Run, runsErr error, status airelay.SessionStatus, statusErr error, tail airelay.Result, tailErr error) ProjectProgress {
	evidence := progressEvidence{
		Status:          status,
		StatusError:     statusErr,
		Tail:            tail.Stdout,
		TailError:       tailErr,
		ComponentErrors: []string{},
	}
	appendComponentError(&evidence.ComponentErrors, "plan", planErr)
	appendComponentError(&evidence.ComponentErrors, "tasks", tasksErr)
	appendComponentError(&evidence.ComponentErrors, "runs", runsErr)
	if statusErr != nil && !status.ControllerReachable {
		appendComponentError(&evidence.ComponentErrors, "agent_status", statusErr)
	}
	appendComponentError(&evidence.ComponentErrors, "agent_tail", tailErr)
	if status.Error != "" && !status.ControllerReachable {
		appendComponentCode(&evidence.ComponentErrors, "agent_status_unavailable")
	}
	if tasksErr == nil && len(tasks) > 0 {
		latestTask := tasks[0].Task
		latestState := tasks[0].State
		evidence.LatestTask = &latestTask
		evidence.LatestTaskState = &latestState
	}
	if runsErr == nil && len(runs) > 0 {
		latestRun := runs[0]
		evidence.LatestRun = &latestRun
	}
	active := []model.Run{}
	if runsErr == nil {
		for _, run := range runs {
			if operationalActiveRun(run) {
				active = append(active, run)
			}
		}
	}
	if planErr == nil && plan.ActiveRunID != "" {
		for i := range active {
			if active[i].ID == plan.ActiveRunID {
				evidence.ActiveRun = &active[i]
				break
			}
		}
	}
	if evidence.ActiveRun == nil && len(active) == 1 {
		evidence.ActiveRun = &active[0]
	}
	if evidence.ActiveRun != nil {
		if tasksErr == nil {
			for i := range tasks {
				if tasks[i].Task.ID == evidence.ActiveRun.TaskID {
					task := tasks[i].Task
					state := tasks[i].State
					evidence.ActiveTask = &task
					evidence.TaskState = &state
					break
				}
			}
		}
		events, eventErr := s.readOperationalEvents(evidence.ActiveRun.ID)
		evidence.Events = events
		appendComponentError(&evidence.ComponentErrors, "operational_events", eventErr)
		if eventErr != nil {
			evidence.Events = []model.RunOperationalEvent{}
		}
		if evidence.ActiveRun.CompletionPath != "" {
			if _, statErr := os.Stat(evidence.ActiveRun.CompletionPath); statErr == nil {
				evidence.Completion = true
			}
		}
		evidence.Compaction = inspectCompaction(*evidence.ActiveRun, evidence.Tail, evidence.Events)
	}
	if hasComponentError(evidence, "plan", "tasks", "runs") {
		return progressSnapshot(evidence, 0, time.Now().UTC())
	}
	return progressSnapshot(evidence, len(active), time.Now().UTC())
}

func progressSnapshot(e progressEvidence, activeCount int, now time.Time) ProjectProgress {
	state, blocker, action := classifyProgressSnapshot(e, activeCount, now)
	activityRun := e.ActiveRun
	if activityRun == nil {
		activityRun = e.LatestRun
	}
	last := lastMeaningfulActivity(activityRun, e.Events)
	age := int64(0)
	if last != nil && now.After(*last) {
		age = int64(now.Sub(*last) / time.Second)
	}
	warnings := boundedProgressWarnings(e.Status.CapacityWarnings)
	return ProjectProgress{
		LatestTask:                       progressSummary(e.LatestTask, e.LatestTaskState),
		LatestRun:                        progressRunSummary(e.LatestRun),
		AgentState:                       state,
		ControllerReachable:              e.Status.ControllerReachable,
		AirelayVersion:                   e.Status.AirelayVersion,
		ProtocolVersion:                  e.Status.ProtocolVersion,
		CapacityWarnings:                 warnings,
		ExitCode:                         e.Status.ExitCode,
		Error:                            progressError(e),
		LastMeaningfulActivity:           last,
		LastMeaningfulActivityAgeSeconds: age,
		Tail:                             e.Tail,
		BlockerClassification:            blocker,
		RecommendedNextAction:            action,
		ComponentErrors:                  append([]string{}, e.ComponentErrors...),
	}
}

func boundedProgressWarnings(values []string) []string {
	result := make([]string, 0, minInt(len(values), maxProgressWarnings))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if len(value) > maxProgressWarningBytes {
			value = value[:maxProgressWarningBytes]
		}
		if value == "" {
			continue
		}
		result = append(result, value)
		if len(result) == maxProgressWarnings {
			break
		}
	}
	sort.Strings(result)
	return result
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func classifyProgressSnapshot(e progressEvidence, activeCount int, now time.Time) (string, string, string) {
	if hasComponentError(e, "plan", "tasks", "runs") {
		return model.AgentStateUnknown, "PROGRESS_COMPONENT_ERROR", "inspect_project_status"
	}
	return classifyProgress(e, activeCount, now)
}

func progressError(e progressEvidence) string {
	if len(e.ComponentErrors) > 0 {
		return "progress_component_unavailable"
	}
	if e.StatusError != nil && !e.Status.ControllerReachable {
		return "controller_unreachable"
	}
	if e.TailError != nil && strings.TrimSpace(e.Tail) == "" {
		return "tail_unavailable"
	}
	if e.Status.Error != "" {
		return "session_error"
	}
	return ""
}

func worktreeHasConflict(status string) bool {
	if status == "" {
		return false
	}
	for _, line := range strings.Split(status, "\n") {
		if strings.HasPrefix(line, "u ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && strings.ContainsAny(fields[1], "UAD") {
				return true
			}
		}
		if len(line) >= 2 && (strings.ContainsAny(line[:2], "U") || strings.HasPrefix(line, "AA") || strings.HasPrefix(line, "DD")) {
			return true
		}
	}
	return false
}

func canonicalResumeMessage(task model.Task, run model.Run) string {
	return fmt.Sprintf("Recovery: re-read gpt-tunnel task read %s; inspect declared branch/base, HEAD/worktree/commits and run state. Resume from durable evidence; preserve scope; skip completed phases; verify, push, and finalize if complete.", task.ID)
}

type RunResumeResult struct {
	RunID               string `json:"run_id"`
	CompactionEventID   string `json:"compaction_event_id"`
	State               string `json:"state"`
	Sent                bool   `json:"sent"`
	ExitCode            int    `json:"exit_code"`
	ControllerReachable bool   `json:"controller_reachable"`
	MessageDigest       string `json:"message_digest"`
	Error               string `json:"error,omitempty"`
}
