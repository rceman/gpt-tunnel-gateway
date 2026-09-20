package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/entity"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

const (
	JournalMigrationActor   = "journal-migration"
	JournalMigrationRole    = "system"
	JournalMigrationSession = "journal-migration"
)

type JournalMigrateCuratedInput struct {
	Source string          `json:"source"`
	Stream string          `json:"stream,omitempty"`
	Data   json.RawMessage `json:"data"`
}

type JournalMigrateInboxTaskInput struct {
	Key           string `json:"key"`
	ArchiveReason string `json:"archive_reason"`
}

type JournalMigrateInput struct {
	ProjectID  string                         `json:"project_id"`
	Curated    []JournalMigrateCuratedInput   `json:"curated,omitempty"`
	InboxTasks []JournalMigrateInboxTaskInput `json:"inbox_tasks,omitempty"`
}

type JournalMigrateResult struct {
	Entries       []model.JournalEntry `json:"entries"`
	ArchivedTasks []string             `json:"archived_tasks,omitempty"`
}

type journalMigrationPendingEntry struct {
	stream        model.JournalStream
	data          json.RawMessage
	operationSeed string
}

type journalMigrationPendingArchive struct {
	key    string
	reason string
}

func (s *Service) JournalMigrate(ctx context.Context, in JournalMigrateInput) (JournalMigrateResult, error) {
	if s.Durability == nil {
		return JournalMigrateResult{}, fmt.Errorf("journal migration requires Shared durability")
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return JournalMigrateResult{}, fmt.Errorf("journal migration project: %w", err)
	}
	project, err := s.EffectiveProjectConfig(in.ProjectID)
	if err != nil || model.ValidateProjectCode(project.ProjectCode) != nil {
		return JournalMigrateResult{}, fmt.Errorf("journal migration project %q has no local project code", in.ProjectID)
	}
	pendingEntries := make([]journalMigrationPendingEntry, 0)
	pendingArchives := make([]journalMigrationPendingArchive, 0, len(in.InboxTasks))
	seen := map[string]bool{}
	for index, curated := range in.Curated {
		stream := model.JournalStream(strings.TrimSpace(curated.Stream))
		if stream == "" {
			stream = model.JournalStreamPlannerNotes
		}
		if stream != model.JournalStreamPlannerNotes {
			return JournalMigrateResult{}, fmt.Errorf("curated[%d]: legacy re-expression must use planner-notes", index)
		}
		if _, err := model.JournalStreamContractFor(string(stream)); err != nil {
			return JournalMigrateResult{}, fmt.Errorf("curated[%d]: %w", index, err)
		}
		source, err := s.journalMigrateSource(ctx, in.ProjectID, project.ProjectCode, curated.Source)
		if err != nil {
			return JournalMigrateResult{}, fmt.Errorf("curated[%d]: %w", index, err)
		}
		data, err := journalMigrateInjectSource(curated.Data, stream, source)
		if err != nil {
			return JournalMigrateResult{}, fmt.Errorf("curated[%d]: %w", index, err)
		}
		if violations := model.ValidateJournalStreamData(stream, data); len(violations) > 0 {
			return JournalMigrateResult{}, fmt.Errorf("curated[%d]: %w", index, journalViolationsError{violations: violations})
		}
		pendingEntries = append(pendingEntries, journalMigrationPendingEntry{
			stream:        stream,
			data:          data,
			operationSeed: "journal-migrate-" + source,
		})
	}
	for index, inbox := range in.InboxTasks {
		if err := model.ValidateCanonicalTaskID(inbox.Key); err != nil {
			return JournalMigrateResult{}, fmt.Errorf("inbox_tasks[%d]: invalid Task key", index)
		}
		wantStream := ""
		switch inbox.Key {
		case "GTW-TSK609":
			wantStream = string(model.JournalStreamLeadFriction)
		case "GTW-TSK619":
			wantStream = string(model.JournalStreamWorkerLessons)
		default:
			return JournalMigrateResult{}, fmt.Errorf("inbox_tasks[%d]: only GTW-TSK609 and GTW-TSK619 are approved cutover inboxes", index)
		}
		if !strings.Contains(inbox.ArchiveReason, wantStream) {
			return JournalMigrateResult{}, fmt.Errorf("inbox_tasks[%d] %s: archive_reason must name canonical stream %q", index, inbox.Key, wantStream)
		}
		if seen[inbox.Key] {
			return JournalMigrateResult{}, fmt.Errorf("inbox_tasks[%d]: duplicate Task key %q", index, inbox.Key)
		}
		seen[inbox.Key] = true
		task, err := s.journalMigrateTask(ctx, inbox.Key)
		if err != nil {
			return JournalMigrateResult{}, fmt.Errorf("inbox_tasks[%d] %s: %w", index, inbox.Key, err)
		}
		if task.ProjectID != in.ProjectID {
			return JournalMigrateResult{}, fmt.Errorf("inbox_tasks[%d] %s: Task is outside project %q", index, inbox.Key, in.ProjectID)
		}
		keys := make([]string, 0, len(task.Metadata))
		for key := range task.Metadata {
			if strings.HasPrefix(key, "lesson_") || strings.HasPrefix(key, "lead_feedback_") {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			var stream model.JournalStream
			var data json.RawMessage
			var parseErr error
			if strings.HasPrefix(key, "lesson_") {
				stream = model.JournalStreamWorkerLessons
				data, parseErr = journalMigrateParseLesson(task.Metadata[key], inbox.Key, key)
			} else {
				stream = model.JournalStreamLeadFriction
				data, parseErr = journalMigrateParseLeadFeedback(task.Metadata[key], inbox.Key, key)
			}
			if parseErr != nil {
				return JournalMigrateResult{}, fmt.Errorf("inbox_tasks[%d] %s metadata %s: %w", index, inbox.Key, key, parseErr)
			}
			if violations := model.ValidateJournalStreamData(stream, data); len(violations) > 0 {
				return JournalMigrateResult{}, fmt.Errorf("inbox_tasks[%d] %s metadata %s: %w", index, inbox.Key, key, journalViolationsError{violations: violations})
			}
			pendingEntries = append(pendingEntries, journalMigrationPendingEntry{
				stream:        stream,
				data:          data,
				operationSeed: "journal-migrate-" + inbox.Key + ":" + key,
			})
		}
		reason := strings.TrimSpace(inbox.ArchiveReason)
		if reason == "" {
			return JournalMigrateResult{}, fmt.Errorf("inbox_tasks[%d] %s: archive_reason is required", index, inbox.Key)
		}
		pendingArchives = append(pendingArchives, journalMigrationPendingArchive{
			key:    inbox.Key,
			reason: reason,
		})
	}
	result := JournalMigrateResult{
		Entries:       make([]model.JournalEntry, 0, len(pendingEntries)),
		ArchivedTasks: make([]string, 0, len(pendingArchives)),
	}
	for index, pending := range pendingEntries {
		entry, err := s.journalMigrateCreate(ctx, in.ProjectID, project.ProjectCode, pending.stream, pending.data, pending.operationSeed)
		if err != nil {
			return result, fmt.Errorf("journal entry %d: %w", index, err)
		}
		result.Entries = append(result.Entries, entry)
	}
	for _, pending := range pendingArchives {
		if _, err := s.TaskLifecycleArchive(ctx, in.ProjectID, pending.key, JournalMigrationActor, pending.reason); err != nil {
			return result, fmt.Errorf("%s archive: %w", pending.key, err)
		}
		result.ArchivedTasks = append(result.ArchivedTasks, pending.key)
	}
	return result, nil
}

func (s *Service) journalMigrateSource(ctx context.Context, projectID, projectCode, source string) (string, error) {
	if err := model.ValidateJournalID(source); err != nil {
		return "", fmt.Errorf("legacy cutover source must be a JRN ID: %q", source)
	}
	var event model.OperatorJournalEvent
	record, err := s.entityRegistry(projectID).ReadInto(ctx, entity.JournalFamily, source, &event)
	if err != nil {
		return "", fmt.Errorf("legacy journal source %s: %w", source, err)
	}
	if _, err := validateOperatorEventPathIdentity(record.Path, s.operatorEventsPrefix(projectID), event, projectID, projectCode); err != nil {
		return "", fmt.Errorf("legacy journal source %s: %w", source, err)
	}
	return source, nil
}

func journalMigrateInjectSource(data json.RawMessage, stream model.JournalStream, source string) (json.RawMessage, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("curated data is required")
	}
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return nil, fmt.Errorf("curated data must be a closed object")
	}
	field := "references"
	if stream == model.JournalStreamWorkerLessons || stream == model.JournalStreamLeadFriction {
		field = "evidence"
	}
	var values []string
	if raw, ok := fields[field]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, fmt.Errorf("curated %s must be a string array", field)
		}
	}
	present := false
	for _, value := range values {
		if value == source {
			present = true
		}
	}
	if !present {
		values = append([]string{source}, values...)
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	fields[field] = encoded
	return json.Marshal(fields)
}

func (s *Service) journalMigrateCreate(ctx context.Context, projectID, projectCode string, stream model.JournalStream, data json.RawMessage, operationSeed string) (model.JournalEntry, error) {
	digest := sha256.Sum256([]byte(projectID + "\x00" + projectCode + "\x00" + string(stream) + "\x00" + string(data)))
	operationID := operationSeed + "-" + hex.EncodeToString(digest[:8])
	created := s.durableNow()
	var entry model.JournalEntry
	_, _, payload, err := s.Durability.CommitSharedLifecycleCreate(ctx, sqlitestore.SharedLifecycleCreate{
		OperationID:         operationID,
		EntityType:          "journal",
		ProjectID:           projectID,
		ProjectCode:         projectCode,
		InitialNextNumber:   1,
		Kind:                "journal-migrate",
		HistoryMutationKind: "create",
		Actor:               JournalMigrationActor,
		Reason:              "journal cutover",
		ChangedFields:       []string{"stream", "data"},
		CreatedAt:           created,
		BuildPayload: func(entityID string) ([]byte, error) {
			_, number, parseErr := model.ParseJournalID(entityID)
			if parseErr != nil {
				return nil, parseErr
			}
			entry = model.JournalEntry{
				SchemaVersion: model.SchemaVersion,
				ID:            entityID,
				ProjectID:     projectID,
				Status:        model.JournalStatusPublished,
				Stream:        stream,
				Data:          append(json.RawMessage(nil), data...),
				Actor:         JournalMigrationActor,
				Role:          JournalMigrationRole,
				SessionID:     JournalMigrationSession,
				Sequence:      number,
				CreatedAt:     created,
			}
			payload, marshalErr := json.Marshal(entry)
			if marshalErr != nil {
				return nil, marshalErr
			}
			if err := model.ValidateJournalEntry(entry); err != nil {
				return nil, err
			}
			return payload, nil
		},
	})
	if err != nil {
		return model.JournalEntry{}, err
	}
	if err := json.Unmarshal(payload, &entry); err != nil {
		return model.JournalEntry{}, fmt.Errorf("decode migrated journal: %w", err)
	}
	return entry, nil
}

func (s *Service) journalMigrateTask(ctx context.Context, key string) (model.TaskAuthoring, error) {
	entity, err := s.Durability.ReadSharedTask(ctx, key)
	if err != nil {
		return model.TaskAuthoring{}, err
	}
	var task model.TaskAuthoring
	if err := json.Unmarshal(entity.Payload, &task); err != nil {
		return model.TaskAuthoring{}, fmt.Errorf("decode shared Task %s: %w", key, err)
	}
	if task.ID != key {
		return model.TaskAuthoring{}, fmt.Errorf("shared Task %s identity mismatch", key)
	}
	return task, nil
}

var journalMigrateTaskRefRE = regexp.MustCompile(`[A-Z]{3}-TSK[1-9][0-9]*`)

func journalMigrateParseLesson(value, taskKey, metadataKey string) (json.RawMessage, error) {
	segments := map[string]string{}
	for _, part := range strings.Split(value, "|") {
		key, text, found := strings.Cut(strings.TrimSpace(part), ":")
		if !found {
			continue
		}
		segments[strings.ToUpper(strings.TrimSpace(key))] = strings.TrimSpace(text)
	}
	for _, required := range []string{"MISTAKE", "WHY", "PREVENTION", "SCOPE", "EVIDENCE"} {
		if segments[required] == "" {
			return nil, fmt.Errorf("lesson metadata lacks %s", required)
		}
	}
	taskRef := ""
	if taskSegment := segments["TASK"]; taskSegment != "" {
		taskRef = journalMigrateTaskRefRE.FindString(taskSegment)
	}
	data := map[string]any{
		"mistake":    segments["MISTAKE"],
		"why":        segments["WHY"],
		"prevention": segments["PREVENTION"],
		"scope":      segments["SCOPE"],
		"evidence":   []string{segments["EVIDENCE"], taskKey + ":" + metadataKey},
	}
	if taskRef != "" {
		data["task"] = taskRef
	}
	if taskSegment := segments["TASK"]; taskSegment != "" && taskSegment != taskRef {
		data["evidence"] = append(data["evidence"].([]string), "TASK: "+taskSegment)
	}
	return json.Marshal(data)
}

var journalMigrateFeedbackLabelRE = regexp.MustCompile(`(Action/workflow|Observed cost|Problem|Evidence|Impact|Improve):\s*`)

func journalMigrateParseLeadFeedback(value, taskKey, metadataKey string) (json.RawMessage, error) {
	matches := journalMigrateFeedbackLabelRE.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("lead feedback metadata has no labeled segments")
	}
	segments := map[string]string{}
	for index, match := range matches {
		label := value[match[2]:match[3]]
		start := match[1]
		end := len(value)
		if index+1 < len(matches) {
			end = matches[index+1][0]
		}
		segments[label] = strings.TrimSpace(value[start:end])
	}
	problem := segments["Problem"]
	if problem == "" {
		return nil, fmt.Errorf("lead feedback metadata lacks Problem")
	}
	if context := segments["Action/workflow"]; context != "" {
		problem = context + " — " + problem
	}
	evidence := make([]string, 0, 2)
	if text := segments["Evidence"]; text != "" {
		evidence = append(evidence, text)
	}
	evidence = append(evidence, taskKey+":"+metadataKey)
	impact := segments["Impact"]
	if cost := segments["Observed cost"]; cost != "" {
		if impact == "" {
			impact = "Observed cost: " + cost
		} else {
			impact += " Observed cost: " + cost
		}
	}
	if impact == "" {
		return nil, fmt.Errorf("lead feedback metadata lacks Impact")
	}
	data := map[string]any{"problem": problem, "evidence": evidence, "impact": impact}
	if improvement := segments["Improve"]; improvement != "" {
		data["proposed_improvement"] = improvement
	}
	return json.Marshal(data)
}
