package model

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"strings"
)

var trainV2SHA256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func ValidateTrainV2CutoverReceipt(v TrainV2CutoverReceipt) error {
	if v.SchemaVersion != TrainV2CutoverSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || v.ExecutionModel != "train_v2" || v.ConfigurationRevision < 1 {
		return fmt.Errorf("invalid train v2 cutover identity")
	}
	if !shaRE.MatchString(v.SourceHead) || !shaRE.MatchString(v.RuntimeHead) {
		return fmt.Errorf("invalid train v2 cutover heads")
	}
	if v.ActionSchemaRevision < 1 || v.HistoricalCompatibility != "preserved" || !v.MaterializationAcknowledged || !v.PlanRetirementAcknowledged {
		return fmt.Errorf("invalid train v2 cutover evidence")
	}
	if strings.TrimSpace(v.NextAction) == "" || strings.ContainsAny(v.NextAction, "\x00\r\n") || v.UpdatedBy == "" || strings.ContainsAny(v.UpdatedBy, "\x00\r\n") || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid train v2 cutover metadata")
	}
	return nil
}

func ValidateTrainV2StartRecord(v TrainV2StartRecord) error {
	if v.SchemaVersion != TrainV2StartSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || v.Status != TrainV2StartActive || ValidateBranch(v.IntegrationBranch) != nil || !shaRE.MatchString(v.BaseRevision) || ValidateBranch(v.LaneBranch) != nil || ValidateCanonicalTaskID(v.CurrentTaskID) != nil || v.CurrentItemPosition < 0 || v.CurrentAttemptNumber < 1 || v.CurrentTaskRevision < 1 || !trainV2SHA256RE.MatchString(v.CurrentTaskRevisionSHA256) || v.StartedAt.IsZero() {
		return fmt.Errorf("invalid train v2 start record")
	}
	if _, _, err := ParseTrainV2ID(v.TrainID); err != nil {
		return fmt.Errorf("invalid train v2 start train ID")
	}
	return nil
}

func ValidateTrainV2(v TrainV2) error {
	code, number, err := ParseTrainV2ID(v.ID)
	if err != nil || code == "" || number < 1 || v.SchemaVersion != TrainV2SchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || v.Revision < 1 || v.CreatedBy == "" || strings.ContainsAny(v.CreatedBy, "\x00\r\n") || v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid train v2 identity")
	}
	switch v.Status {
	case TrainV2Planned, TrainV2Running, TrainV2Paused, TrainV2Blocked, TrainV2ReadyForIntegration, TrainV2Completed, TrainV2RecoveryQuarantined, TrainV2Retired:
	default:
		return fmt.Errorf("invalid train v2 status")
	}
	if len(v.Items) < 1 || len(v.Items) > MaxTrainV2Items {
		return fmt.Errorf("invalid train v2 item count")
	}
	if v.FullProof != nil {
		if err := validateTrainV2FullProof(*v.FullProof); err != nil {
			return err
		}
	}
	if len(v.ReviewResolutions) > MaxTrainV2ReviewResolutionFindings {
		return fmt.Errorf("too many train v2 review resolutions")
	}
	seenResolutions := map[string]bool{}
	for _, resolution := range v.ReviewResolutions {
		if err := ValidateTrainV2ReviewResolution(resolution); err != nil {
			return err
		}
		if resolution.ProjectID != v.ProjectID || resolution.TrainID != v.ID || seenResolutions[resolution.ID] {
			return fmt.Errorf("invalid train v2 review resolution ownership")
		}
		seenResolutions[resolution.ID] = true
	}
	if v.Status == TrainV2Retired {
		if v.Retirement == nil {
			return fmt.Errorf("retired train requires retirement evidence")
		}
		if err := validateTrainV2Retirement(*v.Retirement); err != nil {
			return err
		}
	} else if v.Retirement != nil {
		return fmt.Errorf("non-retired train has retirement evidence")
	}
	if v.Historical != nil {
		if v.Status != TrainV2RecoveryQuarantined && v.Status != TrainV2Retired {
			return fmt.Errorf("historical Train must be non-runnable")
		}
		if err := validateTrainV2HistoricalDisposition(*v.Historical); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for position, item := range v.Items {
		if item.Position != position || ValidateCanonicalTaskID(item.TaskID) != nil || item.TaskRevision < 1 || !trainV2SHA256RE.MatchString(item.TaskRevisionSHA256) || item.AddedAt.IsZero() {
			return fmt.Errorf("invalid train v2 item %d", position)
		}
		if err := validateTrainV2ItemExecution(item); err != nil {
			return fmt.Errorf("item %d: %w", position, err)
		}
		if seen[item.TaskID] {
			return fmt.Errorf("duplicate train v2 task %q", item.TaskID)
		}
		seen[item.TaskID] = true
	}
	return nil
}

func validateTrainV2HistoricalDisposition(v TrainV2HistoricalDisposition) error {
	if v.Kind != TrainV2HistoricalDispositionKind || strings.TrimSpace(v.SourcePath) == "" || strings.HasPrefix(v.SourcePath, "/") || strings.Contains(v.SourcePath, "..") || !strings.Contains(v.SourcePath, "/trains-v2/") || !strings.HasSuffix(v.SourcePath, ".json") || !trainV2SHA256RE.MatchString(v.SourceSHA256) || strings.TrimSpace(v.Reason) == "" || strings.ContainsAny(v.Reason, "\x00\r\n") || len(v.Reason) > 512 || v.MarkedAt.IsZero() {
		return fmt.Errorf("invalid Train-v2 historical disposition")
	}
	return nil
}

func validateTrainV2Retirement(v TrainV2Retirement) error {
	switch v.PreviousStatus {
	case TrainV2Planned, TrainV2Running, TrainV2Paused, TrainV2Blocked, TrainV2RecoveryQuarantined:
	default:
		return fmt.Errorf("invalid retired train previous status")
	}
	if strings.TrimSpace(v.Classification) == "" || strings.ContainsAny(v.Classification, "\x00\r\n") || len(v.Classification) > 64 || strings.TrimSpace(v.Reason) == "" || strings.ContainsAny(v.Reason, "\x00\r\n") || len(v.Reason) > 512 || strings.TrimSpace(v.ActorSessionID) == "" || strings.ContainsAny(v.ActorSessionID, "\x00\r\n") || len(v.ActorSessionID) > 128 || v.RetiredAt.IsZero() {
		return fmt.Errorf("invalid train retirement evidence")
	}
	return nil
}

func validateTrainV2FullProof(proof TrainV2FullProof) error {
	if !shaRE.MatchString(proof.CandidateHead) || proof.RecordedAt.IsZero() || len(proof.GateResults) == 0 {
		return fmt.Errorf("invalid train v2 full proof")
	}
	return ValidateServerGateEvidence(proof.GateResults)
}

func validateTrainV2ItemExecution(item TrainV2Item) error {
	if len(item.Attempts) > 0 {
		if item.ActiveAttemptNumber > uint64(len(item.Attempts)) || item.SuccessfulAttemptNumber > uint64(len(item.Attempts)) {
			return fmt.Errorf("attempt pointer is outside item attempts")
		}
		for i, attempt := range item.Attempts {
			if err := ValidateTrainV2Attempt(attempt); err != nil {
				return fmt.Errorf("attempt %d: %w", i, err)
			}
			if attempt.Number != uint64(i+1) {
				return fmt.Errorf("attempt numbers must be contiguous and item-local")
			}
		}
		return nil
	}
	if item.Status != TrainV2ItemQueued {
		return fmt.Errorf("non-queued Train item requires canonical Attempts")
	}
	switch item.Status {
	case TrainV2ItemQueued:
		if item.Proof != nil || item.Review != nil {
			return fmt.Errorf("queued item has execution state")
		}
	case TrainV2ItemRunning, TrainV2ItemFinalized, TrainV2ItemReviewed, TrainV2ItemBlocked:
		return fmt.Errorf("non-queued Train item requires canonical Attempts")
	default:
		return fmt.Errorf("invalid train v2 item status")
	}
	return nil
}

func ValidateTrainV2Attempt(v TrainV2Attempt) error {
	if v.Number == 0 || v.Status == "" || ValidateObjectIdentifier(v.AgentID) != nil || strings.TrimSpace(v.AirelaySessionKey) == "" || strings.ContainsAny(v.AirelaySessionKey, "\x00\r\n") || ValidateObjectIdentifier(v.GatewayID) != nil || !shaRE.MatchString(v.StartHead) || v.StartedAt.IsZero() {
		return fmt.Errorf("invalid train v2 attempt identity")
	}
	switch v.Status {
	case TrainV2AttemptRunning:
		if v.FinishedAt != nil {
			return fmt.Errorf("running attempt is finished")
		}
	case TrainV2AttemptSucceeded, TrainV2AttemptFailed, TrainV2AttemptAborted, TrainV2AttemptRecovered:
		if v.FinishedAt == nil || v.FinishedAt.IsZero() {
			return fmt.Errorf("terminal attempt requires finished_at")
		}
	default:
		return fmt.Errorf("invalid train v2 attempt status")
	}
	if v.LegacyRunRef != nil {
		if ValidateCanonicalRunID(v.LegacyRunRef.RunID) != nil || !trainV2SHA256RE.MatchString(v.LegacyRunRef.RecordSHA256) || strings.TrimSpace(v.LegacyRunRef.Path) == "" || strings.HasPrefix(v.LegacyRunRef.Path, "/") || strings.Contains(v.LegacyRunRef.Path, "..") {
			return fmt.Errorf("invalid legacy Run evidence reference")
		}
	}
	if v.ReviewID != "" && ValidateObjectIdentifier(v.ReviewID) != nil {
		return fmt.Errorf("invalid train v2 attempt review_id")
	}
	return nil
}

func ValidateTrainV2AttemptReview(v TrainV2AttemptReview) error {
	if v.SchemaVersion != TrainV2AttemptSchemaVersion || ValidateObjectIdentifier(v.ID) != nil {
		return fmt.Errorf("invalid train v2 Attempt review identity")
	}
	if _, _, err := ParseTrainV2ID(v.TrainID); err != nil || ValidateCanonicalTaskID(v.TaskID) != nil || v.ItemPosition < 0 || v.AttemptNumber == 0 || ValidateReviewOutcome(v.Outcome) != nil || !shaRE.MatchString(v.ReviewedHead) || v.ReviewedAt.IsZero() {
		return fmt.Errorf("invalid train v2 Attempt review identity")
	}
	if len(v.Findings) > 128 || len(v.ScopeCoverage) > 128 {
		return fmt.Errorf("train v2 Attempt review is too large")
	}
	return nil
}

func ValidateRunRetirementRecord(v RunRetirementRecord) error {
	if v.SchemaVersion != TrainV2AttemptSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || strings.TrimSpace(v.SourcePath) == "" || strings.HasPrefix(v.SourcePath, "/") || strings.Contains(v.SourcePath, "..") || !strings.HasSuffix(v.SourcePath, "/run.json") || !strings.Contains(v.SourcePath, "/runs/") {
		return fmt.Errorf("invalid Train-v2 Run retirement source")
	}
	if !trainV2SHA256RE.MatchString(v.SourceSHA256) || ValidateObjectIdentifier(v.OriginalRunID) != nil || (v.OriginalRunTaskID != "" && ValidateObjectIdentifier(v.OriginalRunTaskID) != nil) || strings.TrimSpace(v.OriginalRunStatus) == "" {
		return fmt.Errorf("invalid Train-v2 Run retirement identity")
	}
	raw, err := base64.StdEncoding.DecodeString(v.OriginalRunJSONB64)
	if err != nil || len(raw) == 0 {
		return fmt.Errorf("invalid Train-v2 Run retirement bytes")
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != v.SourceSHA256 || path.Base(v.SourcePath) != "run.json" {
		return fmt.Errorf("Train-v2 Run retirement digest/path mismatch")
	}
	return nil
}

func ValidateRunRetirementReceipt(v RunRetirementReceipt) error {
	if v.SchemaVersion != TrainV2AttemptSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || v.State != "completed" || !shaRE.MatchString(v.HubBefore) || !shaRE.MatchString(v.HubAfter) || strings.TrimSpace(v.Reason) == "" || v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() || len(v.Records) > 4096 {
		return fmt.Errorf("invalid Train-v2 Run retirement receipt")
	}
	seen := make(map[string]struct{}, len(v.Records))
	for _, record := range v.Records {
		if err := ValidateRunRetirementRecord(record); err != nil {
			return err
		}
		if _, ok := seen[record.SourcePath]; ok {
			return fmt.Errorf("duplicate Train-v2 Run retirement source")
		}
		seen[record.SourcePath] = struct{}{}
	}
	return nil
}

func ValidateTrainV2LegacyStateMigrationRecord(v TrainV2LegacyStateMigrationRecord) error {
	if ValidateObjectIdentifier(v.TrainID) != nil || strings.TrimSpace(v.TrainPath) == "" || strings.HasPrefix(v.TrainPath, "/") || strings.Contains(v.TrainPath, "..") || !strings.Contains(v.TrainPath, "/trains-v2/") || !strings.HasSuffix(v.TrainPath, ".json") || !trainV2SHA256RE.MatchString(v.TrainSHA256) {
		return fmt.Errorf("invalid Train-v2 legacy migration Train identity")
	}
	trainRaw, err := base64.StdEncoding.DecodeString(v.OriginalTrainJSONB64)
	if err != nil || len(trainRaw) == 0 || digestBytes(trainRaw) != v.TrainSHA256 {
		return fmt.Errorf("Train-v2 legacy migration Train digest mismatch")
	}
	if v.Action != "mark_historical" && v.Action != "retire_stale" && v.Action != "recover_integration" {
		return fmt.Errorf("invalid Train-v2 legacy migration action")
	}
	if v.Action == "recover_integration" {
		if strings.TrimSpace(v.IntegrationPath) == "" || strings.HasPrefix(v.IntegrationPath, "/") || strings.Contains(v.IntegrationPath, "..") || !strings.HasSuffix(v.IntegrationPath, ".integration-operation.json") || !trainV2SHA256RE.MatchString(v.IntegrationSHA256) {
			return fmt.Errorf("invalid Train-v2 integration migration identity")
		}
		opRaw, decodeErr := base64.StdEncoding.DecodeString(v.OriginalIntegrationJSONB64)
		if decodeErr != nil || len(opRaw) == 0 || digestBytes(opRaw) != v.IntegrationSHA256 {
			return fmt.Errorf("Train-v2 integration migration digest mismatch")
		}
		if strings.TrimSpace(v.MutationPath) == "" || strings.HasPrefix(v.MutationPath, "/") || strings.Contains(v.MutationPath, "..") || !strings.HasPrefix(v.MutationPath, "operations/mutations/") || !strings.HasSuffix(v.MutationPath, ".json") || !trainV2SHA256RE.MatchString(v.MutationSHA256) {
			return fmt.Errorf("invalid Train-v2 mutation migration identity")
		}
		mutationRaw, mutationDecodeErr := base64.StdEncoding.DecodeString(v.OriginalMutationJSONB64)
		if mutationDecodeErr != nil || len(mutationRaw) == 0 || digestBytes(mutationRaw) != v.MutationSHA256 {
			return fmt.Errorf("Train-v2 mutation migration digest mismatch")
		}
	}
	return nil
}

func ValidateTrainV2LegacyStateMigrationReceipt(v TrainV2LegacyStateMigrationReceipt) error {
	if v.SchemaVersion != TrainV2AttemptSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || (v.State != "pending" && v.State != "completed") || !shaRE.MatchString(v.HubBefore) || (v.State == "completed" && !shaRE.MatchString(v.HubAfter)) || strings.TrimSpace(v.Reason) == "" || v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() || len(v.Records) > 4096 {
		return fmt.Errorf("invalid Train-v2 legacy migration receipt")
	}
	seen := make(map[string]struct{}, len(v.Records))
	seenTrainIDs := make(map[string]struct{}, len(v.Records))
	for _, record := range v.Records {
		if err := ValidateTrainV2LegacyStateMigrationRecord(record); err != nil {
			return err
		}
		if _, ok := seenTrainIDs[record.TrainID]; ok {
			return fmt.Errorf("duplicate Train-v2 legacy migration Train ID")
		}
		seenTrainIDs[record.TrainID] = struct{}{}
		if _, ok := seen[record.TrainPath]; ok {
			return fmt.Errorf("duplicate Train-v2 legacy migration Train")
		}
		seen[record.TrainPath] = struct{}{}
	}
	return nil
}

func digestBytes(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validateTrainV2ImplementationProof(proof TrainV2ImplementationProof) error {
	if !shaRE.MatchString(proof.CheckpointHead) || !shaRE.MatchString(proof.ImplementationSHA) || proof.ReportID == "" || proof.RecordedAt.IsZero() {
		return fmt.Errorf("invalid train v2 implementation proof")
	}
	if err := ValidateServerGateEvidence(proof.GateResults); err != nil {
		return err
	}
	return nil
}
