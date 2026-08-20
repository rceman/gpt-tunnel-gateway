package model

import (
	"fmt"
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
