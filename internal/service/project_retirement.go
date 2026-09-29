package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

const (
	maxProjectRetirementHubRecords  = 4096
	projectRetirementHubReadTimeout = 2 * time.Second
)

var (
	ErrProjectConfigurationMigrationDeferred = errors.New("ProjectConfiguration migration is deferred for the debug retirement transition")
	ErrInvalidHubProjectRetirement           = errors.New("invalid Hub project retirement authority")
)

type DebugProjectRetirementResult struct {
	ProjectID                string `json:"project_id"`
	Status                   string `json:"status"`
	ConfigurationRevision    int    `json:"configuration_revision"`
	CancelledPublications    int    `json:"cancelled_config_publications"`
	AlreadyRetired           bool   `json:"already_retired"`
	LegacyCallbackEpochCount int    `json:"legacy_callback_epoch_count,omitempty"`
}

func (s *Service) DebugRetireProject(ctx context.Context, projectID, reason string) (DebugProjectRetirementResult, error) {
	if s == nil || s.Durability == nil || !s.Config.Debug.Enabled {
		return DebugProjectRetirementResult{}, fmt.Errorf("debug project retirement is unavailable")
	}
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return DebugProjectRetirementResult{}, err
	}
	if projectID == config.GTWProjectID {
		return DebugProjectRetirementResult{}, sqlitestore.ErrProjectRetirementUnsafe
	}
	now := time.Now().UTC()
	preliminary := model.ProjectRetirement{
		SchemaVersion:               model.ProjectRetirementSchemaVersion,
		ProjectID:                   projectID,
		Revision:                    1,
		Reason:                      reason,
		Actor:                       "gatewayd",
		RetiredAt:                   now,
		CancelledConfigOutboxSHA256: sha256Hex(nil),
	}
	if err := model.ValidateProjectRetirement(preliminary); err != nil {
		return DebugProjectRetirementResult{}, err
	}
	retirement, sharedFound, err := s.Durability.ReadSharedProjectRetirement(ctx, projectID)
	if err != nil {
		return DebugProjectRetirementResult{}, err
	}
	localRetirement, localFound, err := s.Durability.ReadLocalProjectRetirement(ctx, projectID)
	if err != nil {
		return DebugProjectRetirementResult{}, err
	}
	if localFound && localRetirement.Reason != reason {
		return DebugProjectRetirementResult{}, sqlitestore.ErrProjectRetirementConflict
	}
	if sharedFound && retirement.Reason != reason {
		return DebugProjectRetirementResult{}, sqlitestore.ErrProjectRetirementConflict
	}
	hubRetirement, hubFound, err := s.readHubProjectRetirement(ctx, projectID)
	if err != nil {
		return DebugProjectRetirementResult{}, err
	}
	if hubFound && sharedFound && !sameProjectRetirementRecord(hubRetirement, retirement) {
		return DebugProjectRetirementResult{}, sqlitestore.ErrProjectRetirementConflict
	}
	if hubFound && !sharedFound {
		retirement = hubRetirement
		if retirement.Reason != reason {
			return DebugProjectRetirementResult{}, sqlitestore.ErrProjectRetirementConflict
		}
	}
	debugTransition := !sharedFound && !hubFound
	if debugTransition {
		if localFound {
			preliminary.RetiredAt = localRetirement.RetiredAt
		} else {
			present, err := s.projectRetirementSourceExists(ctx, projectID)
			if err != nil {
				return DebugProjectRetirementResult{}, err
			}
			if !present {
				return DebugProjectRetirementResult{}, fmt.Errorf("project retirement target has no active local, Hub, or Shared authority")
			}
		}
		evidence, err := s.Durability.ReadProjectRetirementLegacyEpochEvidence(ctx, projectID)
		if err != nil {
			return DebugProjectRetirementResult{}, err
		}
		preliminary.LegacyCallbackEpochCount = evidence.Count
		preliminary.LegacyCallbackEpochSHA256 = evidence.SHA256
		retirement = preliminary
		now = retirement.RetiredAt
	}
	if debugTransition {
		err = s.Durability.BeginDebugProjectRetirementWithLegacyEpochEvidence(ctx, retirement)
	} else {
		err = s.Durability.BeginLocalProjectRetirement(ctx, retirement)
	}
	if err != nil {
		return DebugProjectRetirementResult{}, err
	}
	created := false
	if hubFound && !sharedFound {
		if err := s.Durability.ImportSharedProjectRetirement(ctx, retirement); err != nil {
			return DebugProjectRetirementResult{}, err
		}
		sharedFound = true
	} else if !sharedFound {
		evidence := sqlitestore.ProjectRetirementLegacyEpochEvidence{
			Count: retirement.LegacyCallbackEpochCount, SHA256: retirement.LegacyCallbackEpochSHA256,
		}
		retirement, created, err = s.Durability.RetireSharedProjectWithLegacyEpochEvidence(ctx, projectID, reason, now, evidence)
		if err != nil {
			return DebugProjectRetirementResult{}, err
		}
		sharedFound = true
	}
	if err := s.publishHubProjectRetirement(ctx, retirement); err != nil && !errors.Is(err, errSharedOutboxNoop) {
		return DebugProjectRetirementResult{}, err
	}
	if entry, found, err := s.Durability.ReadSharedOutboxEntry(ctx, "project-retirement-"+projectID); err != nil {
		return DebugProjectRetirementResult{}, err
	} else if found && entry.PublishedAt == "" {
		if err := s.Durability.MarkOutboxPublished(ctx, entry.ID, time.Now().UTC()); err != nil {
			return DebugProjectRetirementResult{}, err
		}
	}
	if err := s.removeManagedProject(projectID); err != nil {
		return DebugProjectRetirementResult{}, err
	}
	return DebugProjectRetirementResult{
		ProjectID:                projectID,
		Status:                   "retired",
		ConfigurationRevision:    retirement.ConfigurationRevision,
		CancelledPublications:    retirement.CancelledConfigPublications,
		AlreadyRetired:           localFound || !created && (sharedFound || hubFound),
		LegacyCallbackEpochCount: retirement.LegacyCallbackEpochCount,
	}, nil
}

func (s *Service) DebugMigrateProjectConfigurations(ctx context.Context) error {
	if s == nil || s.Durability == nil || !s.Config.Debug.Enabled {
		return fmt.Errorf("debug ProjectConfiguration migration is unavailable")
	}
	if err := s.SyncProjectRetirements(ctx); err != nil {
		return err
	}
	if err := s.Durability.MigrateProjectConfigurationToCanonical(ctx); err != nil {
		return fmt.Errorf("migrate Shared ProjectConfiguration: %w", err)
	}
	if err := s.MigrateHubProjectConfigurations(ctx); err != nil {
		return fmt.Errorf("migrate Hub ProjectConfiguration: %w", err)
	}
	return nil
}

func (s *Service) SyncProjectRetirements(ctx context.Context) error {
	if s == nil || s.Durability == nil {
		return fmt.Errorf("Shared and Hub are required for project retirement synchronization")
	}
	hubRecords, err := s.listHubProjectRetirements(ctx)
	if err != nil {
		return err
	}
	for _, record := range hubRecords {
		shared, found, err := s.Durability.ReadSharedProjectRetirement(ctx, record.ProjectID)
		if err != nil {
			return err
		}
		if found && !sameProjectRetirementRecord(shared, record) {
			return sqlitestore.ErrProjectRetirementConflict
		}
		if err := s.Durability.BeginLocalProjectRetirement(ctx, record); err != nil {
			return err
		}
		if !found {
			if err := s.Durability.ImportSharedProjectRetirement(ctx, record); err != nil {
				return err
			}
		}
		if err := s.removeManagedProject(record.ProjectID); err != nil {
			return err
		}
	}
	localRecords, err := s.Durability.ListLocalProjectRetirements(ctx)
	if err != nil {
		return err
	}
	for _, local := range localRecords {
		record, found, err := s.Durability.ReadSharedProjectRetirement(ctx, local.ProjectID)
		if err != nil {
			return err
		}
		if found && record.Reason != local.Reason {
			return sqlitestore.ErrProjectRetirementConflict
		}
		if !found {
			evidence, err := s.Durability.ReadProjectRetirementLegacyEpochEvidence(ctx, local.ProjectID)
			if err != nil {
				return err
			}
			record = model.ProjectRetirement{
				SchemaVersion: model.ProjectRetirementSchemaVersion, ProjectID: local.ProjectID, Revision: 1,
				Reason: local.Reason, Actor: "gatewayd", RetiredAt: local.RetiredAt,
				CancelledConfigOutboxSHA256: sha256Hex(nil),
				LegacyCallbackEpochCount:    evidence.Count, LegacyCallbackEpochSHA256: evidence.SHA256,
			}
			if err := s.Durability.BeginLocalProjectRetirement(ctx, record); err != nil {
				return err
			}
			record, _, err = s.Durability.RetireSharedProjectWithLegacyEpochEvidence(ctx, local.ProjectID, local.Reason, local.RetiredAt, evidence)
			if err != nil {
				return err
			}
		}
	}
	sharedRecords, err := s.Durability.ListSharedProjectRetirements(ctx)
	if err != nil {
		return err
	}
	for _, record := range sharedRecords {
		if err := s.Durability.BeginLocalProjectRetirement(ctx, record); err != nil {
			return err
		}
		if err := s.removeManagedProject(record.ProjectID); err != nil {
			return err
		}
		if err := s.publishHubProjectRetirement(ctx, record); err != nil && !errors.Is(err, errSharedOutboxNoop) {
			return fmt.Errorf("publish Hub retirement for %q: %w", record.ProjectID, err)
		}
		if entry, found, err := s.Durability.ReadSharedOutboxEntry(ctx, "project-retirement-"+record.ProjectID); err != nil {
			return err
		} else if found && entry.PublishedAt == "" {
			if err := s.Durability.MarkOutboxPublished(ctx, entry.ID, time.Now().UTC()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) publishSharedProjectRetirement(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	if s == nil || s.Durability == nil || entry.EntityType != "project_retirement" || entry.Kind != "project-retirement" || entry.Revision != 1 || entry.EntityID == "" || len(entry.Payload) == 0 {
		return fmt.Errorf("invalid project retirement outbox entry")
	}
	var retirement model.ProjectRetirement
	if err := decodeStrict(entry.Payload, &retirement); err != nil || model.ValidateProjectRetirement(retirement) != nil || retirement.ProjectID != entry.EntityID {
		return fmt.Errorf("invalid project retirement outbox payload")
	}
	shared, found, err := s.Durability.ReadSharedProjectRetirement(ctx, retirement.ProjectID)
	if err != nil || !found || !sameProjectRetirementRecord(shared, retirement) {
		if err != nil {
			return err
		}
		return sqlitestore.ErrProjectRetirementConflict
	}
	return s.publishHubProjectRetirement(ctx, retirement)
}

func (s *Service) publishHubProjectRetirement(ctx context.Context, retirement model.ProjectRetirement) error {
	if err := model.ValidateProjectRetirement(retirement); err != nil {
		return err
	}
	markerPath := s.projectRetirementPath(retirement.ProjectID)
	paths := []string{s.projectPath(retirement.ProjectID), s.projectIdentifiersPath(retirement.ProjectID), s.projectConfigurationPath(retirement.ProjectID), s.workflowPolicyPath(retirement.ProjectID)}
	_, err := s.Hub.Transact(ctx, "", "gateway: retire project "+retirement.ProjectID, func(worktree string) ([]string, error) {
		markerFile := filepath.Join(worktree, filepath.FromSlash(markerPath))
		markerData, readErr := os.ReadFile(markerFile)
		markerExists := readErr == nil
		if readErr != nil && !os.IsNotExist(readErr) {
			return nil, readErr
		}
		if markerExists {
			var existing model.ProjectRetirement
			if err := decodeStrict(markerData, &existing); err != nil || model.ValidateProjectRetirement(existing) != nil || !sameProjectRetirementRecord(existing, retirement) {
				return nil, sqlitestore.ErrProjectRetirementConflict
			}
		}
		changed := make([]string, 0, len(paths)+1)
		for _, path := range paths {
			removed, err := hub.RemoveFile(worktree, path)
			if err != nil {
				return nil, err
			}
			if removed {
				changed = append(changed, path)
			}
		}
		canonicalMarker, err := json.Marshal(retirement)
		if err != nil {
			return nil, err
		}
		var compactMarker bytes.Buffer
		if markerExists {
			if err := json.Compact(&compactMarker, markerData); err != nil {
				return nil, err
			}
		}
		if !markerExists || !bytes.Equal(compactMarker.Bytes(), canonicalMarker) {
			if err := hub.WriteJSON(worktree, markerPath, retirement); err != nil {
				return nil, err
			}
			changed = append(changed, markerPath)
		}
		if len(changed) == 0 {
			return nil, errSharedOutboxNoop
		}
		return changed, nil
	})
	if err == nil {
		return nil
	}
	snapshot, snapshotErr := s.Hub.ReadSnapshot(ctx)
	if snapshotErr != nil {
		return fmt.Errorf("%w: read committed Hub snapshot: %v", err, snapshotErr)
	}
	defer snapshot.Close()
	markerData, snapshotErr := snapshot.ReadFile(ctx, markerPath)
	if snapshotErr != nil {
		if IsNotFound(snapshotErr) {
			return err
		}
		return fmt.Errorf("%w: inspect Hub retirement marker: %v", err, snapshotErr)
	}
	var existing model.ProjectRetirement
	if decodeStrict(markerData, &existing) != nil || model.ValidateProjectRetirement(existing) != nil || !sameProjectRetirementRecord(existing, retirement) {
		return err
	}
	for _, path := range paths {
		if _, readErr := snapshot.ReadFile(ctx, path); readErr == nil {
			return fmt.Errorf("%w: Hub retirement path %s remains present", err, path)
		} else if !IsNotFound(readErr) {
			return fmt.Errorf("%w: inspect Hub retirement path %s: %v", err, path, readErr)
		}
	}
	return errSharedOutboxNoop
}

func (s *Service) readHubProjectRetirementBounded(ctx context.Context, projectID string) (model.ProjectRetirement, bool, error) {
	readCtx, cancel := context.WithTimeout(ctx, projectRetirementHubReadTimeout)
	defer cancel()
	return s.readHubProjectRetirement(readCtx, projectID)
}

func (s *Service) readHubProjectRetirement(ctx context.Context, projectID string) (model.ProjectRetirement, bool, error) {
	if s == nil {
		return model.ProjectRetirement{}, false, nil
	}
	data, err := s.Hub.ReadFile(ctx, s.projectRetirementPath(projectID))
	if err != nil {
		if IsNotFound(err) {
			return model.ProjectRetirement{}, false, nil
		}
		return model.ProjectRetirement{}, false, err
	}
	var retirement model.ProjectRetirement
	if err := decodeStrict(data, &retirement); err != nil || model.ValidateProjectRetirement(retirement) != nil || retirement.ProjectID != projectID {
		return model.ProjectRetirement{}, false, ErrInvalidHubProjectRetirement
	}
	return retirement, true, nil
}

func (s *Service) listHubProjectRetirements(ctx context.Context) ([]model.ProjectRetirement, error) {
	if s == nil {
		return nil, nil
	}
	prefix := hub.ProtocolRoot + "/projects"
	paths, err := s.Hub.List(ctx, prefix, "/retirement.json")
	if err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(paths) > maxProjectRetirementHubRecords {
		return nil, fmt.Errorf("Hub project retirement enumeration exceeds bounded row maximum")
	}
	retirements := make([]model.ProjectRetirement, 0, len(paths))
	for _, path := range paths {
		if !strings.HasPrefix(path, prefix+"/") || !strings.HasSuffix(path, "/retirement.json") {
			return nil, fmt.Errorf("invalid Hub project retirement path")
		}
		projectID := strings.TrimSuffix(strings.TrimPrefix(path, prefix+"/"), "/retirement.json")
		if model.ValidateProjectIdentifier(projectID) != nil || s.projectRetirementPath(projectID) != path {
			return nil, fmt.Errorf("invalid Hub project retirement identity path")
		}
		retirement, found, err := s.readHubProjectRetirement(ctx, projectID)
		if err != nil || !found {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("Hub project retirement disappeared during enumeration")
		}
		retirements = append(retirements, retirement)
	}
	sort.Slice(retirements, func(i, j int) bool { return retirements[i].ProjectID < retirements[j].ProjectID })
	return retirements, nil
}

func (s *Service) projectRetirementPath(projectID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil {
		return "../invalid-project-retirement"
	}
	return s.projectPrefix(projectID) + "/retirement.json"
}

func (s *Service) projectRetirementSourceExists(ctx context.Context, projectID string) (bool, error) {
	resolution, err := s.resolveProjects()
	if err != nil {
		return false, err
	}
	if _, found := resolution.ManagedProjects[projectID]; found {
		return true, nil
	}
	if _, found := s.Config.Projects[projectID]; found {
		return true, nil
	}
	var project model.Project
	err = s.Hub.ReadJSON(ctx, s.projectPath(projectID), &project)
	if err == nil {
		if err := model.ValidateProject(project); err != nil || project.ID != projectID {
			return false, fmt.Errorf("invalid Hub project retirement target")
		}
		return true, nil
	}
	if !IsNotFound(err) {
		return false, err
	}
	if data, err := s.Hub.ReadFile(ctx, s.projectConfigurationPath(projectID)); err == nil {
		var identity struct {
			SchemaVersion int    `json:"schema_version"`
			ProjectID     string `json:"project_id"`
			Revision      int    `json:"revision"`
		}
		if json.Unmarshal(data, &identity) != nil || identity.SchemaVersion < 1 || identity.SchemaVersion > model.ProjectConfigurationSchemaVersion || identity.ProjectID != projectID || identity.Revision < 1 {
			return false, fmt.Errorf("invalid Hub ProjectConfiguration retirement target")
		}
		return true, nil
	} else if !IsNotFound(err) {
		return false, err
	}
	return s.Durability.HasProjectConfigurationRetirementSource(ctx, projectID)
}

func (s *Service) removeManagedProject(projectID string) error {
	registry, err := config.LoadManagedProjects(s.Config.StateDir)
	if err != nil {
		return err
	}
	if _, found := registry.Projects[projectID]; !found {
		return nil
	}
	digest, err := registry.Digest()
	if err != nil {
		return err
	}
	next := registry
	next.Projects = make(map[string]config.ManagedProjectEntry, len(registry.Projects)-1)
	for id, project := range registry.Projects {
		if id != projectID {
			next.Projects[id] = project
		}
	}
	next.Revision++
	_, err = config.WriteManagedProjectRegistry(s.Config.StateDir, digest, next)
	return err
}

func (s *Service) isProjectRetired(ctx context.Context, projectID string) (bool, error) {
	if model.ValidateProjectIdentifier(projectID) != nil {
		return false, fmt.Errorf("invalid project retirement selector")
	}
	if s.Durability == nil {
		return false, nil
	}
	if _, found, err := s.Durability.ReadLocalProjectRetirement(ctx, projectID); err != nil {
		return false, err
	} else if found {
		return true, nil
	}
	_, found, err := s.Durability.ReadSharedProjectRetirement(ctx, projectID)
	return found, err
}

func sameProjectRetirementRecord(left, right model.ProjectRetirement) bool {
	leftData, leftErr := json.Marshal(left)
	rightData, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftData, rightData)
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
