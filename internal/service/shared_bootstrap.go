package service

import (
	"context"
	"fmt"
	"path"
	"sort"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

const maxSharedBootstrapRecords = 1000

// BootstrapSharedFromHub imports one bounded, pinned local Hub snapshot into
// the Shared projections. It is an explicit post-ready/bootstrap operation;
// authoring workers only consume the resulting projections and never call Hub.
func (s *Service) BootstrapSharedFromHub(ctx context.Context) error {
	if s.Durability == nil {
		return fmt.Errorf("shared store is unavailable")
	}
	snapshot, err := s.Hub.ReadLocalSnapshot(ctx)
	if err != nil {
		return err
	}
	defer snapshot.Close()
	projectIDs := make([]string, 0, len(s.Config.Projects))
	for projectID := range s.Config.Projects {
		if model.ValidateProjectIdentifier(projectID) == nil {
			projectIDs = append(projectIDs, projectID)
		}
	}
	sort.Strings(projectIDs)
	for _, projectID := range projectIDs {
		if err := s.bootstrapSharedProjectConfiguration(ctx, snapshot, projectID); err != nil {
			return err
		}
		if err := s.bootstrapSharedAgents(ctx, snapshot, projectID); err != nil {
			return err
		}
		if err := s.bootstrapSharedWatcherGuide(ctx, snapshot, projectID); err != nil {
			return err
		}
		if err := s.bootstrapSharedTasks(ctx, snapshot, projectID); err != nil {
			return err
		}
		if err := s.bootstrapSharedADRs(ctx, snapshot, projectID); err != nil {
			return err
		}
		if err := s.bootstrapSharedJournals(ctx, snapshot, projectID); err != nil {
			return err
		}
		if err := s.bootstrapSharedIntegrationOperations(ctx, snapshot, projectID); err != nil {
			return err
		}
		if err := s.bootstrapSharedTrains(ctx, snapshot, projectID); err != nil {
			return err
		}
		if err := s.Durability.MarkSharedBootstrapComplete(ctx, sqlitestore.SharedBootstrapMarker{ProjectID: projectID, HubRevision: snapshot.Revision(), CompletedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) bootstrapSharedJournals(ctx context.Context, snapshot *hub.ReadSnapshot, projectID string) error {
	paths, err := snapshot.List(ctx, s.operatorEventsPrefix(projectID), ".json")
	if err != nil {
		if IsNotFound(err) {
			return s.Durability.PutSharedJournalSequence(ctx, projectID, s.Config.Projects[projectID].ProjectCode, 1)
		}
		return err
	}
	if len(paths) > maxSharedBootstrapRecords {
		return fmt.Errorf("Shared operator journal bootstrap exceeds bounded record limit")
	}
	files, err := snapshot.ReadFiles(ctx, paths)
	if err != nil {
		return err
	}
	projectCode := s.Config.Projects[projectID].ProjectCode
	if model.ValidateProjectCode(projectCode) != nil {
		return fmt.Errorf("project %q has no valid local project code for Shared journal bootstrap", projectID)
	}
	next := uint64(1)
	for _, filePath := range paths {
		var event model.OperatorJournalEvent
		if err := decodeStrict(files[filePath], &event); err != nil {
			return fmt.Errorf("decode operator journal bootstrap %s: %w", filePath, err)
		}
		number, err := validateOperatorEventPathIdentity(filePath, s.operatorEventsPrefix(projectID), event, projectID, projectCode)
		if err != nil {
			return fmt.Errorf("invalid operator journal bootstrap %s: %w", filePath, err)
		}
		if number >= next {
			next = number + 1
		}
		if err := s.Durability.PutSharedProjection(ctx, "journal", sqlitestore.SharedEntity{ID: event.ID, Revision: projectionRevision(event.RecordedAt), Payload: files[filePath], UpdatedAt: event.RecordedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
			return err
		}
		if event.SupersedesEventID != "" {
			if err := s.Durability.PutSharedJournalSupersession(ctx, event.SupersedesEventID, "bootstrap-journal-"+event.ID); err != nil {
				return err
			}
		}
	}
	return s.Durability.PutSharedJournalSequence(ctx, projectID, projectCode, int64(next))
}

func (s *Service) bootstrapSharedIntegrationOperations(ctx context.Context, snapshot *hub.ReadSnapshot, projectID string) error {
	paths, err := snapshot.List(ctx, s.projectPrefix(projectID)+"/trains-v2", ".integration-operation.json")
	if err != nil {
		if IsNotFound(err) {
			return nil
		}
		return err
	}
	if len(paths) > maxSharedBootstrapRecords {
		return fmt.Errorf("Shared integration-operation bootstrap exceeds bounded record limit")
	}
	files, err := snapshot.ReadFiles(ctx, paths)
	if err != nil {
		return err
	}
	for _, filePath := range paths {
		var operation trainv2.IntegrationOperation
		if err := decodeStrict(files[filePath], &operation); err != nil {
			return fmt.Errorf("decode integration operation bootstrap %s: %w", filePath, err)
		}
		if operation.ProjectID != projectID {
			return fmt.Errorf("invalid integration operation bootstrap %s", filePath)
		}
		if _, _, err := model.ParseTrainV2ID(operation.TrainID); err != nil {
			return fmt.Errorf("invalid integration operation bootstrap %s: %w", filePath, err)
		}
		if err := trainv2.ValidateIntegrationOperation(operation); err != nil {
			return fmt.Errorf("invalid integration operation bootstrap %s: %w", filePath, err)
		}
		if err := s.Durability.PutSharedProjection(ctx, "integration_operation", sqlitestore.SharedEntity{ID: sharedIntegrationOperationID(operation.ProjectID, operation.TrainID), Revision: 1, Payload: files[filePath], UpdatedAt: operation.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) bootstrapSharedWatcherGuide(ctx context.Context, snapshot *hub.ReadSnapshot, projectID string) error {
	path := s.watcherGuidePath(projectID)
	files, err := snapshot.ReadFiles(ctx, []string{path})
	if err != nil {
		if IsNotFound(err) {
			return nil
		}
		return err
	}
	if len(files[path]) == 0 {
		return nil
	}
	var guide model.WatcherGuide
	if err := decodeStrict(files[path], &guide); err != nil {
		return fmt.Errorf("decode watcher guide bootstrap %s: %w", path, err)
	}
	if guide.ProjectID != projectID {
		return fmt.Errorf("watcher guide bootstrap project mismatch %s", path)
	}
	if err := model.ValidateWatcherGuide(guide); err != nil {
		return fmt.Errorf("invalid watcher guide bootstrap %s: %w", path, err)
	}
	return s.Durability.PutSharedProjection(ctx, "watcher_guide", sqlitestore.SharedEntity{
		ID: guide.ProjectID, Revision: int64(guide.Revision), Payload: files[path], UpdatedAt: guide.UpdatedAt.UTC().Format(time.RFC3339Nano),
	})
}

func (s *Service) bootstrapSharedAgents(ctx context.Context, snapshot *hub.ReadSnapshot, projectID string) error {
	paths, err := snapshot.List(ctx, s.projectPrefix(projectID)+"/agents", ".json")
	if err != nil {
		return err
	}
	if len(paths) > maxSharedBootstrapRecords {
		return fmt.Errorf("Shared Agent bootstrap exceeds bounded record limit")
	}
	files, err := snapshot.ReadFiles(ctx, paths)
	if err != nil {
		return err
	}
	for _, filePath := range paths {
		var agent model.Agent
		if err := decodeStrict(files[filePath], &agent); err != nil {
			return fmt.Errorf("decode Agent bootstrap %s: %w", filePath, err)
		}
		if agent.ProjectID != projectID || model.ValidateAgent(agent) != nil {
			return fmt.Errorf("invalid Agent bootstrap %s", filePath)
		}
		if err := s.Durability.PutSharedProjection(ctx, "agent", sqlitestore.SharedEntity{ID: agent.ProjectID + "\x00" + agent.AgentID, Revision: projectionRevision(agent.UpdatedAt), Payload: files[filePath], UpdatedAt: agent.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) bootstrapSharedProjectConfiguration(ctx context.Context, snapshot *hub.ReadSnapshot, projectID string) error {
	path := s.projectConfigurationPath(projectID)
	files, err := snapshot.ReadFiles(ctx, []string{path})
	if err != nil {
		return fmt.Errorf("read project configuration bootstrap %s: %w", path, err)
	}
	var configuration model.ProjectConfiguration
	if err := decodeStrict(files[path], &configuration); err != nil {
		return fmt.Errorf("decode project configuration bootstrap %s: %w", path, err)
	}
	if configuration.ProjectID != projectID {
		return fmt.Errorf("project configuration bootstrap project mismatch %s", path)
	}
	if err := model.ValidateProjectConfiguration(configuration); err != nil {
		return fmt.Errorf("invalid project configuration bootstrap %s: %w", path, err)
	}
	return s.Durability.PutSharedProjection(ctx, "project_configuration", sqlitestore.SharedEntity{
		ID: configuration.ProjectID, Revision: int64(configuration.Revision), Payload: files[path], UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano),
	})
}

func (s *Service) bootstrapSharedTasks(ctx context.Context, snapshot *hub.ReadSnapshot, projectID string) error {
	paths, err := snapshot.List(ctx, s.projectPrefix(projectID)+"/tasks-v2", ".json")
	if err != nil {
		return err
	}
	if len(paths) > maxSharedBootstrapRecords {
		return fmt.Errorf("Shared task bootstrap exceeds bounded record limit")
	}
	files, err := snapshot.ReadFiles(ctx, paths)
	if err != nil {
		return err
	}
	projectCode := s.Config.Projects[projectID].ProjectCode
	if model.ValidateProjectCode(projectCode) != nil {
		return fmt.Errorf("project %q has no valid local project code for Shared bootstrap", projectID)
	}
	nextTaskNumber := int64(1)
	for _, filePath := range paths {
		var task model.TaskAuthoring
		if err := decodeStrict(files[filePath], &task); err != nil {
			return fmt.Errorf("decode task bootstrap %s: %w", filePath, err)
		}
		if task.ProjectID != projectID {
			return fmt.Errorf("invalid task bootstrap %s", filePath)
		}
		if err := model.ValidateTaskAuthoring(task); err != nil {
			return fmt.Errorf("invalid task bootstrap %s: %w", filePath, err)
		}
		number, err := model.ParseTaskIDForProject(task.ID, projectCode)
		if err != nil {
			return fmt.Errorf("invalid task bootstrap identifier %s: %w", filePath, err)
		}
		if number >= uint64(nextTaskNumber) {
			nextTaskNumber = int64(number + 1)
		}
		if err := s.Durability.PutSharedProjection(ctx, "task", sqlitestore.SharedEntity{ID: task.ID, Revision: int64(task.Revision), Payload: files[filePath], UpdatedAt: task.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
			return err
		}
	}
	return s.Durability.PutSharedTaskSequence(ctx, projectID, projectCode, nextTaskNumber)
}

func (s *Service) bootstrapSharedADRs(ctx context.Context, snapshot *hub.ReadSnapshot, projectID string) error {
	paths, err := snapshot.List(ctx, s.projectPrefix(projectID)+"/adrs", ".json")
	if err != nil {
		return err
	}
	if len(paths) > maxSharedBootstrapRecords {
		return fmt.Errorf("Shared ADR bootstrap exceeds bounded record limit")
	}
	files, err := snapshot.ReadFiles(ctx, paths)
	if err != nil {
		return err
	}
	projectCode := s.Config.Projects[projectID].ProjectCode
	if model.ValidateProjectCode(projectCode) != nil {
		return fmt.Errorf("project %q has no valid local project code for Shared bootstrap", projectID)
	}
	nextADRNumber := int64(1)
	for _, filePath := range paths {
		var adr model.ADR
		if err := decodeStrict(files[filePath], &adr); err != nil {
			return fmt.Errorf("decode ADR bootstrap %s: %w", filePath, err)
		}
		if adr.ProjectID != projectID {
			return fmt.Errorf("ADR bootstrap project mismatch %s", filePath)
		}
		if err := model.ValidateADR(adr); err != nil {
			return fmt.Errorf("invalid ADR bootstrap %s: %w", filePath, err)
		}
		number, counted, err := sharedADRBootstrapSequenceNumber(adr.ID, projectCode)
		if err != nil {
			return fmt.Errorf("invalid ADR bootstrap identifier %s: %w", filePath, err)
		}
		if counted && number >= uint64(nextADRNumber) {
			nextADRNumber = int64(number + 1)
		}
		if err := s.Durability.PutSharedProjection(ctx, "adr", sqlitestore.SharedEntity{ID: adr.ID, Revision: projectionRevision(adr.CreatedAt), Payload: files[filePath], UpdatedAt: adr.CreatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
			return err
		}
	}
	return s.Durability.PutSharedADRSequence(ctx, projectID, projectCode, nextADRNumber)
}

func sharedADRBootstrapSequenceNumber(id, projectCode string) (uint64, bool, error) {
	if number, err := model.ParseADRIDForProject(id, projectCode); err == nil {
		return number, true, nil
	}
	if _, number, err := model.ParseHistoricalADRID(id); err == nil {
		return number, true, nil
	}
	if model.ValidateADRIdentifier(id) == nil {
		// Legacy ADR identifiers remain valid stored records, but do not
		// influence the canonical project-local allocator sequence.
		return 0, false, nil
	}
	return 0, false, fmt.Errorf("unsupported ADR identifier")
}

func (s *Service) bootstrapSharedTrains(ctx context.Context, snapshot *hub.ReadSnapshot, projectID string) error {
	root := s.trainV2Root(projectID)
	paths, err := snapshot.List(ctx, root, ".json")
	if err != nil {
		return err
	}
	if len(paths) > maxSharedBootstrapRecords {
		return fmt.Errorf("Shared Train bootstrap exceeds bounded record limit")
	}
	integrationPaths, err := snapshot.List(ctx, root, ".integration.json")
	if err != nil {
		return err
	}
	if len(integrationPaths) > maxSharedBootstrapRecords {
		return fmt.Errorf("Shared integration receipt bootstrap exceeds bounded record limit")
	}
	files, err := snapshot.ReadFiles(ctx, paths)
	if err != nil {
		return err
	}
	for _, filePath := range paths {
		if !canonicalTrainV2RecordName(path.Base(filePath)) {
			continue
		}
		var train model.TrainV2
		if err := decodeStrict(files[filePath], &train); err != nil {
			return fmt.Errorf("decode Train bootstrap %s: %w", filePath, err)
		}
		if train.ProjectID != projectID {
			return fmt.Errorf("Train bootstrap project mismatch %s", filePath)
		}
		if err := model.ValidateTrainV2(train); err != nil {
			return fmt.Errorf("invalid Train bootstrap %s: %w", filePath, err)
		}
		if err := s.Durability.PutSharedProjection(ctx, "train", sqlitestore.SharedEntity{ID: train.ID, Revision: int64(train.Revision), Payload: files[filePath], UpdatedAt: train.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
			return err
		}
	}
	receiptFiles, err := snapshot.ReadFiles(ctx, integrationPaths)
	if err != nil {
		return err
	}
	for _, filePath := range integrationPaths {
		var receipt trainv2.IntegrationReceipt
		if err := decodeStrict(receiptFiles[filePath], &receipt); err != nil {
			return fmt.Errorf("decode integration receipt bootstrap %s: %w", filePath, err)
		}
		if receipt.ProjectID != projectID {
			return fmt.Errorf("integration receipt bootstrap project mismatch %s", filePath)
		}
		if err := trainv2.ValidateIntegrationReceipt(receipt); err != nil {
			return fmt.Errorf("invalid integration receipt bootstrap %s: %w", filePath, err)
		}
		if err := s.Durability.PutSharedIntegrationReceipt(ctx, sqlitestore.SharedIntegrationReceipt{ID: sqlitestore.SharedIntegrationReceiptID(projectID, receipt.TrainID), Revision: projectionRevision(receipt.UpdatedAt), Payload: receiptFiles[filePath], UpdatedAt: receipt.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
			return err
		}
	}
	return nil
}

func projectionRevision(at time.Time) int64 {
	if at.IsZero() {
		return 1
	}
	return at.UnixNano()
}
