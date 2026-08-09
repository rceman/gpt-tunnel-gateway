package service

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const (
	progressTailLines       = 4
	maxOperationalEvents    = 256
	maxOperationalEventFile = 64 << 10
	resumeObservationWindow = 30 * time.Second
	maxProgressWarnings     = 16
	maxProgressWarningBytes = 256
)

var questionRE = regexp.MustCompile(`(?i)(\?|waiting for (your )?input|need(s)? (your )?input|please (choose|confirm|provide|answer)|which (option|approach|path))`)

// ProgressTask is the bounded task projection used by project_status.
type ProgressTask struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// ProgressRun is the bounded run projection used by project_status.  It does
// not contain a session key, dispatch output, or local completion path.
type ProgressRun struct {
	ID           string     `json:"id"`
	TaskID       string     `json:"task_id"`
	Status       string     `json:"status"`
	Branch       string     `json:"branch"`
	BaseRevision string     `json:"base_revision"`
	CreatedAt    time.Time  `json:"created_at"`
	DispatchedAt *time.Time `json:"dispatched_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

// ProjectProgress is the single bounded progress snapshot returned with
// project_status.  Reads do not write operational events or send prompts.
type ProjectProgress struct {
	LatestTask                       *ProgressTask `json:"latest_task,omitempty"`
	LatestRun                        *ProgressRun  `json:"latest_run,omitempty"`
	AgentState                       string        `json:"agent_state"`
	ControllerReachable              bool          `json:"controller_reachable"`
	AirelayVersion                   string        `json:"airelay_version,omitempty"`
	ProtocolVersion                  string        `json:"protocol_version,omitempty"`
	CapacityWarnings                 []string      `json:"capacity_warnings"`
	ExitCode                         int           `json:"exit_code"`
	Error                            string        `json:"error,omitempty"`
	LastMeaningfulActivity           *time.Time    `json:"last_meaningful_activity,omitempty"`
	LastMeaningfulActivityAgeSeconds int64         `json:"last_meaningful_activity_age_seconds"`
	Tail                             string        `json:"tail"`
	BlockerClassification            string        `json:"blocker_classification"`
	RecommendedNextAction            string        `json:"recommended_next_action"`
	ComponentErrors                  []string      `json:"component_errors"`
}

func (p ProjectProgress) MarshalJSON() ([]byte, error) {
	type alias ProjectProgress
	if p.CapacityWarnings == nil {
		p.CapacityWarnings = []string{}
	}
	if p.AgentState == "" {
		p.AgentState = model.AgentStateUnknown
	}
	if p.ComponentErrors == nil {
		p.ComponentErrors = []string{}
	}
	return json.Marshal(alias(p))
}

type compactionObservation struct {
	Detected        bool
	Started         bool
	Completed       bool
	EventID         string
	Marker          string
	MeaningfulAfter bool
	QuestionAfter   bool
	TailDigest      string
}

type progressEvidence struct {
	Status          airelay.SessionStatus
	StatusError     error
	Tail            string
	TailError       error
	Events          []model.RunOperationalEvent
	Compaction      compactionObservation
	ActiveRun       *model.Run
	ActiveTask      *model.Task
	TaskState       *model.TaskState
	Completion      bool
	LatestTask      *model.Task
	LatestTaskState *model.TaskState
	LatestRun       *model.Run
	ComponentErrors []string
}

func appendComponentError(errors *[]string, name string, err error) {
	if err == nil {
		return
	}
	appendComponentCode(errors, name+"_unavailable")
}

func appendComponentCode(errors *[]string, code string) {
	for _, existing := range *errors {
		if existing == code {
			return
		}
	}
	*errors = append(*errors, code)
}

func hasComponentError(e progressEvidence, names ...string) bool {
	for _, want := range names {
		for _, got := range e.ComponentErrors {
			if got == want+"_unavailable" {
				return true
			}
		}
	}
	return false
}

func (s *Service) operationalEventPath(runID string) string {
	return filepath.Join(s.localRunDir(runID), "events.jsonl")
}

func eventLockName(runID string) string {
	digest := sha256.Sum256([]byte(runID))
	return "events-" + hex.EncodeToString(digest[:8])
}

func digestText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func (s *Service) readOperationalEvents(runID string) ([]model.RunOperationalEvent, error) {
	path := s.operationalEventPath(runID)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []model.RunOperationalEvent{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) > maxOperationalEventFile {
		return nil, fmt.Errorf("operational event log exceeds bound")
	}
	events := []model.RunOperationalEvent{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		if len(events) >= maxOperationalEvents {
			return nil, fmt.Errorf("operational event count exceeds bound")
		}
		var event model.RunOperationalEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("invalid operational event: %w", err)
		}
		if err := model.ValidateRunOperationalEvent(event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Service) appendOperationalEvent(event model.RunOperationalEvent) (retErr error) {
	if err := model.ValidateRunOperationalEvent(event); err != nil {
		return err
	}
	if err := fsutil.EnsureDir(s.localRunDir(event.RunID), 0o700); err != nil {
		return err
	}
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), eventLockName(event.RunID))
	if err != nil {
		return err
	}
	defer func() {
		if err := lock.Release(); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()
	current, err := s.readOperationalEvents(event.RunID)
	if err != nil {
		return err
	}
	if len(current) >= maxOperationalEvents {
		return fmt.Errorf("operational event count exceeds bound")
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	line := make([]byte, len(data)+1)
	copy(line, data)
	line[len(data)] = '\n'
	path := s.operationalEventPath(event.RunID)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(existing) > maxOperationalEventFile || len(existing)+len(line) > maxOperationalEventFile {
		return fmt.Errorf("operational event log exceeds bound")
	}
	if info, statErr := os.Lstat(path); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("operational event log is not a regular file")
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	closeWith := func(cause error) error {
		return errors.Join(cause, f.Close())
	}
	if err := f.Chmod(0o600); err != nil {
		return closeWith(err)
	}
	n, err := f.Write(line)
	if err != nil {
		return closeWith(err)
	}
	if n != len(line) {
		return closeWith(io.ErrShortWrite)
	}
	if err := f.Sync(); err != nil {
		return closeWith(err)
	}
	return f.Close()
}

func newOperationalEvent(run model.Run, eventType, compactionID, tail, message string, exitCode int, resultingState string) (model.RunOperationalEvent, error) {
	id, err := model.NewID()
	if err != nil {
		return model.RunOperationalEvent{}, err
	}
	event := model.RunOperationalEvent{
		SchemaVersion:     model.OperationalEventSchemaVersion,
		ID:                id,
		EventType:         eventType,
		RunID:             run.ID,
		TaskID:            run.TaskID,
		ProjectID:         run.ProjectID,
		CompactionEventID: compactionID,
		OccurredAt:        time.Now().UTC(),
		ExitCode:          exitCode,
		ResultingState:    resultingState,
	}
	if tail != "" {
		event.TailDigest = digestText(tail)
	}
	if message != "" {
		event.MessageDigest = digestText(message)
	}
	return event, nil
}

func latestEvent(events []model.RunOperationalEvent, eventType, compactionID string) *model.RunOperationalEvent {
	var found *model.RunOperationalEvent
	for i := range events {
		if events[i].EventType != eventType || (compactionID != "" && events[i].CompactionEventID != compactionID) {
			continue
		}
		if found == nil || events[i].OccurredAt.After(found.OccurredAt) {
			copy := events[i]
			found = &copy
		}
	}
	return found
}

func isCompactionLine(line string) (started, completed bool) {
	lower := strings.ToLower(strings.TrimSpace(line))
	if !strings.Contains(lower, "compact") && !strings.Contains(lower, "context window") {
		return false, false
	}
	if strings.Contains(lower, "low context") || strings.Contains(lower, "% left") || strings.Contains(lower, "context remaining") {
		return false, false
	}
	started = strings.Contains(lower, "compacting") || strings.Contains(lower, "compaction started") || strings.Contains(lower, "context compaction started")
	completed = strings.Contains(lower, "compacted") || strings.Contains(lower, "compaction complete") || strings.Contains(lower, "compaction completed") || strings.Contains(lower, "context window compressed")
	return started, completed
}

func isCompactionAcknowledgement(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return true
	}
	if _, completed := isCompactionLine(lower); completed {
		return true
	}
	for _, prefix := range []string{"ack", "acknowledged", "continuing", "resuming", "resume", "context restored", "context recovery"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func compactionEventID(runID, marker string) string {
	return digestText(runID + "\x00" + strings.ToLower(strings.TrimSpace(marker)))
}

func hasExplicitQuestion(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && questionRE.MatchString(line) {
			return true
		}
	}
	return false
}

func inspectCompaction(run model.Run, tail string, events []model.RunOperationalEvent) compactionObservation {
	lines := strings.Split(strings.TrimRight(tail, "\r\n"), "\n")
	lastIndex := -1
	started, completed := false, false
	marker := ""
	for i, line := range lines {
		st, done := isCompactionLine(line)
		if st || done {
			lastIndex = i
			started, completed = st, done
			marker = strings.TrimSpace(line)
		}
	}
	if lastIndex >= 0 && completed {
		eventID := compactionEventID(run.ID, marker)
		meaningful, question := false, false
		for _, line := range lines[lastIndex+1:] {
			if strings.TrimSpace(line) == "" || isStaticAgentFooterLine(line) || isCompactionAcknowledgement(line) {
				continue
			}
			if questionRE.MatchString(line) {
				question = true
			}
			meaningful = true
		}
		return compactionObservation{
			Detected:        !meaningful && !question,
			Completed:       true,
			EventID:         eventID,
			Marker:          marker,
			MeaningfulAfter: meaningful,
			QuestionAfter:   question,
			TailDigest:      digestText(tail),
		}
	}
	if lastIndex >= 0 && started {
		return compactionObservation{
			Started:    true,
			Marker:     marker,
			TailDigest: digestText(tail),
		}
	}
	// A restart may lose the tail marker.  The durable event log remains the
	// only source used to continue a previously observed compaction event.
	var completedEvent *model.RunOperationalEvent
	for i := range events {
		if events[i].EventType == model.EventCompactionCompleted || events[i].EventType == model.EventResumeSent || events[i].EventType == model.EventResumeCompleted || events[i].EventType == model.EventStalledAfterCompaction {
			if completedEvent == nil || events[i].OccurredAt.After(completedEvent.OccurredAt) {
				copy := events[i]
				completedEvent = &copy
			}
		}
	}
	if completedEvent != nil && completedEvent.CompactionEventID != "" {
		return compactionObservation{
			Detected:   true,
			Completed:  true,
			EventID:    completedEvent.CompactionEventID,
			TailDigest: digestText(tail),
		}
	}
	return compactionObservation{TailDigest: digestText(tail)}
}

func (s *Service) latestProgress(ctx context.Context, projectID string) (progressEvidence, error) {
	local, err := s.projectConfig(projectID)
	if err != nil {
		return progressEvidence{}, err
	}
	plan, err := s.PlanRead(ctx, projectID)
	if err != nil {
		return progressEvidence{}, err
	}
	tasks, err := s.TaskList(ctx, projectID)
	if err != nil {
		return progressEvidence{}, err
	}
	runs, err := s.RunList(ctx, projectID)
	if err != nil {
		return progressEvidence{}, err
	}
	evidence := progressEvidence{}
	if len(tasks) > 0 {
		latestTask := tasks[0].Task
		latestState := tasks[0].State
		evidence.LatestTask = &latestTask
		evidence.LatestTaskState = &latestState
	}
	if len(runs) > 0 {
		run := runs[0]
		evidence.LatestRun = &run
	}
	active := []model.Run{}
	for _, run := range runs {
		if operationalActiveRun(run) {
			active = append(active, run)
		}
	}
	if plan.ActiveRunID != "" {
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
		for i := range tasks {
			if tasks[i].Task.ID == evidence.ActiveRun.TaskID {
				task := tasks[i].Task
				state := tasks[i].State
				evidence.ActiveTask = &task
				evidence.TaskState = &state
				break
			}
		}
		evidence.Events, err = s.readOperationalEvents(evidence.ActiveRun.ID)
		if err != nil {
			return progressEvidence{}, err
		}
		if evidence.ActiveRun.CompletionPath != "" {
			if _, statErr := os.Stat(evidence.ActiveRun.CompletionPath); statErr == nil {
				evidence.Completion = true
			}
		}
		status, statusErr := s.Airelay.Status(ctx, local.AirelaySessionKey)
		evidence.Status, evidence.StatusError = status, statusErr
		tail, tailErr := s.Airelay.Tail(ctx, local.AirelaySessionKey, progressTailLines)
		evidence.Tail, evidence.TailError = tail.Stdout, tailErr
		evidence.Compaction = inspectCompaction(*evidence.ActiveRun, evidence.Tail, evidence.Events)
	} else {
		status, statusErr := s.Airelay.Status(ctx, local.AirelaySessionKey)
		evidence.Status, evidence.StatusError = status, statusErr
		tail, tailErr := s.Airelay.Tail(ctx, local.AirelaySessionKey, progressTailLines)
		evidence.Tail, evidence.TailError = tail.Stdout, tailErr
	}
	return evidence, nil
}

func progressSummary(task *model.Task, state *model.TaskState) *ProgressTask {
	if task == nil || state == nil {
		return nil
	}
	return &ProgressTask{
		ID:        task.ID,
		Title:     task.Title,
		Status:    state.Status,
		CreatedAt: task.CreatedAt,
	}
}

func progressRunSummary(run *model.Run) *ProgressRun {
	if run == nil {
		return nil
	}
	return &ProgressRun{
		ID:           run.ID,
		TaskID:       run.TaskID,
		Status:       run.Status,
		Branch:       run.Branch,
		BaseRevision: run.BaseRevision,
		CreatedAt:    run.CreatedAt,
		DispatchedAt: run.DispatchedAt,
		FinishedAt:   run.FinishedAt,
	}
}

func warningKind(warnings []string) string {
	for _, warning := range warnings {
		lower := strings.ToLower(warning)
		if strings.Contains(lower, "rate limited") || strings.Contains(lower, "rate-limited") || strings.Contains(lower, "too many requests") || strings.Contains(lower, "rate limit exceeded") || strings.Contains(lower, "rate limit unavailable") || strings.Contains(lower, "rate limit rejected") {
			return model.AgentStateRateLimited
		}
	}
	for _, warning := range warnings {
		lower := strings.ToLower(warning)
		if explicitCapacityUnavailable(lower) {
			return model.AgentStateCapacityBlocked
		}
	}
	return ""
}

func explicitCapacityUnavailable(warning string) bool {
	for _, exhausted := range []string{
		"capacity unavailable", "capacity exhausted", "capacity blocked", "capacity rejected", "capacity full", "no capacity",
		"quota exhausted", "quota exceeded", "quota unavailable", "quota blocked",
		"weekly limit exhausted", "weekly limit exceeded", "weekly limit unavailable", "weekly limit blocked",
	} {
		if strings.Contains(warning, exhausted) {
			return true
		}
	}
	return strings.Contains(warning, "0%") && (strings.Contains(warning, "quota") || strings.Contains(warning, "limit") || strings.Contains(warning, "capacity"))
}

func normalIdleTail(tail string) bool {
	if strings.TrimSpace(tail) == "" || hasExplicitQuestion(tail) {
		return false
	}
	lower := strings.ToLower(tail)
	return strings.Contains(lower, "idle") || strings.Contains(lower, "ready") || strings.Contains(lower, "prompt") || strings.Contains(lower, "waiting")
}

func isStaticAgentFooterLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" || lower == "ready" || lower == "idle" || lower == "waiting" || lower == "default prompt" || lower == "idle prompt ready" {
		return true
	}
	for _, prefix := range []string{
		"model:", "context:", "context window:", "workspace:", "working directory:", "cwd:",
		"prompt: default", "prompt: idle", "status: idle", "status: ready", "status: waiting",
		"state: idle", "state: ready", "state: waiting", "controller: reachable",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func meaningfulAgentLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || isStaticAgentFooterLine(line) || isCompactionAcknowledgement(line) {
		return false
	}
	if _, started := isCompactionLine(line); started {
		return false
	}
	if _, completed := isCompactionLine(line); completed {
		return false
	}
	return !hasExplicitQuestion(line)
}

func classifyProgress(e progressEvidence, activeCount int, now time.Time) (string, string, string) {
	if activeCount == 0 {
		if !e.Status.ControllerReachable {
			return model.AgentStateError, "CONTROLLER_UNREACHABLE", "inspect_controller"
		}
		if hasComponentError(e, "agent_status") {
			return model.AgentStateUnknown, "AGENT_STATUS_UNAVAILABLE", "inspect_agent_status"
		}
		if e.Status.State == "running" {
			return model.AgentStateRunning, "none", "wait_for_agent_progress"
		}
		if e.Status.State == "waiting" && hasExplicitQuestion(e.Tail) {
			return model.AgentStateWaitingForInput, "WAITING_FOR_AGENT_INPUT", "answer_agent_question"
		}
		if e.Status.State == "error" && !normalIdleTail(e.Tail) && !e.Status.ControllerReachable {
			return model.AgentStateError, "CONTROLLER_UNREACHABLE", "inspect_controller"
		}
		return model.AgentStateIdle, "none", "await_authorized_task"
	}
	if e.ActiveRun == nil {
		return model.AgentStateUnknown, "MULTIPLE_ACTIVE_RUNS", "repair_operational_state"
	}
	if !e.Status.ControllerReachable {
		return model.AgentStateError, "CONTROLLER_UNREACHABLE", "inspect_controller"
	}
	if hasComponentError(e, "agent_status", "agent_tail") {
		return model.AgentStateUnknown, "AGENT_PROGRESS_UNAVAILABLE", "inspect_agent_status"
	}
	if e.Completion {
		return model.AgentStateFinalizationPending, "FINALIZATION_PENDING", "run_finalize"
	}
	if hasExplicitQuestion(e.Tail) {
		return model.AgentStateWaitingForInput, "WAITING_FOR_AGENT_INPUT", "answer_agent_question"
	}
	if e.Compaction.Started && !e.Compaction.Completed {
		return model.AgentStateCompacting, "COMPACTION_IN_PROGRESS", "wait_for_compaction"
	}
	if e.Compaction.Detected && !e.Compaction.MeaningfulAfter {
		resume := latestEvent(e.Events, model.EventResumeSent, e.Compaction.EventID)
		meaningful := latestEvent(e.Events, model.EventMeaningfulOutput, e.Compaction.EventID)
		if meaningful != nil && (resume == nil || meaningful.OccurredAt.After(resume.OccurredAt)) {
			return model.AgentStateRunning, "none", "wait_for_agent_progress"
		}
		if resume != nil {
			if now.Sub(resume.OccurredAt) >= resumeObservationWindow {
				return model.AgentStateStalled, "STALLED_AFTER_COMPACTION", "review_stalled_after_compaction"
			}
			return model.AgentStateCompactedResuming, "COMPACTION_RESUME_PENDING", "wait_for_agent_progress"
		}
		return model.AgentStateCompactedIdle, "COMPACTION_RECOVERY_AVAILABLE", "run_resume"
	}
	if warning := warningKind(e.Status.CapacityWarnings); warning != "" {
		return warning, strings.ToUpper(warning), "wait_for_capacity"
	}
	if e.ActiveRun.Status == "awaiting_result" && e.Status.State == "waiting" {
		return model.AgentStateCompletionPending, "COMPLETION_PENDING", "inspect_completion_state"
	}
	if e.Status.State == "running" {
		return model.AgentStateRunning, "none", "wait_for_agent_progress"
	}
	if e.Status.State == "waiting" {
		return model.AgentStateWaitingForInput, "WAITING_FOR_AGENT_INPUT", "answer_agent_question"
	}
	if e.Status.State == "idle" || (e.Status.State == "error" && normalIdleTail(e.Tail)) {
		return model.AgentStateStalled, "AGENT_STALLED", "inspect_agent_progress"
	}
	if e.Status.State == "error" || e.StatusError != nil || e.TailError != nil {
		return model.AgentStateError, "CONTROLLER_UNREACHABLE", "inspect_controller"
	}
	return model.AgentStateUnknown, "UNKNOWN_AGENT_STATE", "inspect_agent_progress"
}

func lastMeaningfulActivity(run *model.Run, events []model.RunOperationalEvent) *time.Time {
	var latest *time.Time
	if run != nil {
		when := run.CreatedAt
		if run.DispatchedAt != nil {
			when = *run.DispatchedAt
		}
		latest = &when
	}
	for _, event := range events {
		if event.EventType != model.EventMeaningfulOutput && event.EventType != model.EventResumeCompleted {
			continue
		}
		when := event.OccurredAt
		if latest == nil || when.After(*latest) {
			latest = &when
		}
	}
	return latest
}

func (s *Service) projectProgress(ctx context.Context, projectID string) (ProjectProgress, error) {
	evidence, err := s.latestProgress(ctx, projectID)
	if err != nil {
		return ProjectProgress{}, err
	}
	runs, err := s.RunList(ctx, projectID)
	if err != nil {
		return ProjectProgress{}, err
	}
	activeCount := 0
	for _, run := range runs {
		if operationalActiveRun(run) {
			activeCount++
		}
	}
	now := time.Now().UTC()
	return progressSnapshot(evidence, activeCount, now), nil
}

// projectProgressFromInputs assembles the canonical snapshot from one bounded
// set of already-fetched components. It intentionally reports component error
// codes rather than raw command, path, or controller errors.
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

func (s *Service) RunResume(ctx context.Context, id string) (RunResumeResult, error) {
	return s.runResume(ctx, id, false)
}

func (s *Service) runResume(ctx context.Context, id string, automatic bool) (RunResumeResult, error) {
	run, err := s.findRun(ctx, id)
	if err != nil {
		return RunResumeResult{}, err
	}
	if err := requireCanonicalRun(run); err != nil {
		return RunResumeResult{}, err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return RunResumeResult{}, err
	}
	if !operationalActiveRun(run) {
		return RunResumeResult{}, fmt.Errorf("run is not active")
	}
	task, err := s.findTask(ctx, run.TaskID)
	if err != nil {
		return RunResumeResult{}, err
	}
	local, err := s.projectConfig(run.ProjectID)
	if err != nil {
		return RunResumeResult{}, err
	}
	if local.AirelaySessionKey != run.SessionKey {
		return RunResumeResult{}, fmt.Errorf("run session does not match configured project session")
	}
	lock, err := s.acquireSessionSendLock(local.AirelaySessionKey)
	if err != nil {
		return RunResumeResult{}, fmt.Errorf("agent session resume is already in progress")
	}
	defer func() { _ = lock.Release() }()
	return s.resumeRunLocked(ctx, run, task, local, automatic)
}

func (s *Service) resumeRunLocked(ctx context.Context, run model.Run, task model.Task, local config.ProjectConfig, automatic bool) (RunResumeResult, error) {
	active, err := s.activeOperationalRunsForSession(ctx, run.SessionKey)
	if err != nil {
		return RunResumeResult{}, err
	}
	if active != 1 {
		return RunResumeResult{}, fmt.Errorf("resume requires exactly one active operational run for the session")
	}
	plan, err := s.PlanRead(ctx, run.ProjectID)
	if err != nil {
		return RunResumeResult{}, err
	}
	if plan.ActiveTaskID != task.ID || plan.ActiveRunID != run.ID {
		return RunResumeResult{}, fmt.Errorf("resume requires current plan ownership")
	}
	taskState, err := s.taskState(ctx, task)
	if err != nil {
		return RunResumeResult{}, err
	}
	if taskState.Status == "completed" || taskState.Status == "cancelled" || taskState.Status == "superseded" {
		return RunResumeResult{}, fmt.Errorf("resume requires a non-terminal task")
	}
	if run.CompletionPath != "" {
		if _, statErr := os.Stat(run.CompletionPath); statErr == nil {
			return RunResumeResult{}, fmt.Errorf("resume blocked while completion is pending finalization")
		}
	}
	wt, err := s.Git.WorktreeStatus(ctx, local)
	if err != nil {
		return RunResumeResult{}, err
	}
	if wt.Branch != task.Branch || worktreeHasConflict(wt.Porcelain) {
		return RunResumeResult{}, fmt.Errorf("resume requires the declared task branch and a non-conflicted worktree")
	}
	status, statusErr := s.Airelay.Status(ctx, local.AirelaySessionKey)
	if statusErr != nil && !status.ControllerReachable {
		return RunResumeResult{
			RunID:               run.ID,
			ControllerReachable: false,
			State:               model.AgentStateError,
			Error:               "controller_unreachable",
		}, fmt.Errorf("agent controller is unreachable")
	}
	if !status.ControllerReachable {
		return RunResumeResult{
			RunID:               run.ID,
			ControllerReachable: false,
			State:               model.AgentStateError,
			Error:               "controller_unreachable",
		}, fmt.Errorf("agent controller is unreachable")
	}
	events, err := s.readOperationalEvents(run.ID)
	if err != nil {
		return RunResumeResult{}, err
	}
	tail, tailErr := s.Airelay.Tail(ctx, local.AirelaySessionKey, progressTailLines)
	if tailErr != nil && strings.TrimSpace(tail.Stdout) == "" {
		if !hasDurableCompactionEvidence(run, events) {
			return RunResumeResult{}, fmt.Errorf("unable to observe bounded agent tail")
		}
	}
	observation := inspectCompaction(run, tail.Stdout, events)
	if observation.Started && !observation.Completed {
		if observation.EventID == "" {
			observation.EventID = compactionEventID(run.ID, observation.Marker)
		}
		started, e := newOperationalEvent(run, model.EventCompactionStarted, observation.EventID, tail.Stdout, "", status.ExitCode, model.AgentStateCompacting)
		if e != nil {
			return RunResumeResult{}, e
		}
		if latestEvent(events, model.EventCompactionStarted, observation.EventID) == nil {
			if err := s.appendOperationalEvent(started); err != nil {
				return RunResumeResult{}, err
			}
		}
		return RunResumeResult{}, fmt.Errorf("confirmed resumable compaction is required")
	}
	if !observation.Detected || observation.QuestionAfter || hasExplicitQuestion(tail.Stdout) {
		return RunResumeResult{}, fmt.Errorf("confirmed resumable compaction is required")
	}
	if warning := warningKind(status.CapacityWarnings); warning != "" {
		return RunResumeResult{}, fmt.Errorf("resume blocked by %s", warning)
	}
	if prior := latestEvent(events, model.EventResumeSent, observation.EventID); prior != nil {
		return RunResumeResult{}, fmt.Errorf("resume already sent for compaction event")
	}
	message := canonicalResumeMessage(task, run)
	if len(message) > s.Airelay.MaxMessageBytes {
		return RunResumeResult{}, fmt.Errorf("generated recovery message exceeds bound")
	}
	if completed := latestEvent(events, model.EventMeaningfulOutput, observation.EventID); completed != nil {
		return RunResumeResult{}, fmt.Errorf("resume event has already produced meaningful output")
	}
	observed, err := newOperationalEvent(run, model.EventCompactionObserved, observation.EventID, tail.Stdout, "", status.ExitCode, model.AgentStateCompactedIdle)
	if err != nil {
		return RunResumeResult{}, err
	}
	if latestEvent(events, model.EventCompactionObserved, observation.EventID) == nil {
		if err := s.appendOperationalEvent(observed); err != nil {
			return RunResumeResult{}, err
		}
	}
	compactionEvent, err := newOperationalEvent(run, model.EventCompactionCompleted, observation.EventID, tail.Stdout, "", status.ExitCode, model.AgentStateCompactedIdle)
	if err != nil {
		return RunResumeResult{}, err
	}
	if latestEvent(events, model.EventCompactionCompleted, observation.EventID) == nil {
		if err := s.appendOperationalEvent(compactionEvent); err != nil {
			return RunResumeResult{}, err
		}
	}
	reservation, err := newOperationalEvent(run, model.EventResumeSent, observation.EventID, tail.Stdout, message, -1, model.AgentStateCompactedResuming)
	if err != nil {
		return RunResumeResult{}, err
	}
	if err := s.appendOperationalEvent(reservation); err != nil {
		return RunResumeResult{}, err
	}
	result, promptErr := s.Airelay.Prompt(ctx, local.AirelaySessionKey, message)
	resultState := model.AgentStateCompactedResuming
	resumeResult := RunResumeResult{
		RunID:               run.ID,
		CompactionEventID:   observation.EventID,
		State:               resultState,
		Sent:                promptErr == nil,
		ExitCode:            result.ExitCode,
		ControllerReachable: status.ControllerReachable,
		MessageDigest:       digestText(message),
	}
	if promptErr != nil {
		failed, e := newOperationalEvent(run, model.EventResumeFailed, observation.EventID, "", message, result.ExitCode, model.AgentStateError)
		if e != nil {
			return resumeResult, e
		}
		if e := s.appendOperationalEvent(failed); e != nil {
			return resumeResult, e
		}
		resumeResult.Error = "resume delivery failed"
		return resumeResult, fmt.Errorf("resume delivery failed")
	}
	completedEvent, e := newOperationalEvent(run, model.EventResumeCompleted, observation.EventID, "", message, result.ExitCode, resultState)
	if e != nil {
		return resumeResult, e
	}
	if e := s.appendOperationalEvent(completedEvent); e != nil {
		return resumeResult, e
	}
	return resumeResult, nil
}

func hasDurableCompactionEvidence(run model.Run, events []model.RunOperationalEvent) bool {
	observation := inspectCompaction(run, "", events)
	return observation.Detected || observation.Started
}

func (s *Service) activeOperationalRunsForSession(ctx context.Context, session string) (int, error) {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, project := range projects {
		runs, err := s.RunList(ctx, project.ID)
		if err != nil {
			return 0, err
		}
		for _, candidate := range runs {
			if candidate.SessionKey == session && operationalActiveRun(candidate) {
				count++
			}
		}
	}
	return count, nil
}

func meaningfulTailAfterResume(tail string) bool {
	for _, line := range strings.Split(tail, "\n") {
		line = strings.TrimSpace(line)
		if !meaningfulAgentLine(line) {
			continue
		}
		return true
	}
	return false
}

func (s *Service) observeResumeProgress(ctx context.Context, run model.Run, now time.Time) error {
	events, err := s.readOperationalEvents(run.ID)
	if err != nil {
		return err
	}
	resume := latestEvent(events, model.EventResumeSent, "")
	if resume == nil {
		return nil
	}
	local, err := s.projectConfig(run.ProjectID)
	if err != nil {
		return err
	}
	tail, err := s.Airelay.Tail(ctx, local.AirelaySessionKey, progressTailLines)
	if err != nil && strings.TrimSpace(tail.Stdout) == "" {
		return nil
	}
	if digestText(tail.Stdout) != resume.TailDigest && meaningfulTailAfterResume(tail.Stdout) {
		meaningful, eventErr := newOperationalEvent(run, model.EventMeaningfulOutput, resume.CompactionEventID, tail.Stdout, "", tail.ExitCode, model.AgentStateRunning)
		if eventErr != nil {
			return eventErr
		}
		if latestEvent(events, model.EventMeaningfulOutput, resume.CompactionEventID) == nil {
			return s.appendOperationalEvent(meaningful)
		}
		return nil
	}
	if now.Sub(resume.OccurredAt) >= resumeObservationWindow && latestEvent(events, model.EventStalledAfterCompaction, resume.CompactionEventID) == nil {
		stalled, eventErr := newOperationalEvent(run, model.EventStalledAfterCompaction, resume.CompactionEventID, tail.Stdout, "", tail.ExitCode, model.AgentStateStalled)
		if eventErr != nil {
			return eventErr
		}
		return s.appendOperationalEvent(stalled)
	}
	return nil
}
