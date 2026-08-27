package model

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
)

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
func ValidateTrainV2LegacyStateMigrationRecord(v TrainV2LegacyStateMigrationRecord) error {
	if ValidateObjectIdentifier(v.TrainID) != nil || strings.TrimSpace(v.TrainPath) == "" || strings.HasPrefix(v.TrainPath, "/") || strings.Contains(v.TrainPath, "..") || !strings.Contains(v.TrainPath, "/trains-v2/") || !strings.HasSuffix(v.TrainPath, ".json") || !trainV2SHA256RE.MatchString(v.TrainSHA256) {
		return fmt.Errorf("invalid Train-v2 legacy migration Train identity")
	}
	trainRaw, err := base64.StdEncoding.DecodeString(v.OriginalTrainJSONB64)
	if err != nil || len(trainRaw) == 0 || digestBytes(trainRaw) != v.TrainSHA256 {
		return fmt.Errorf("Train-v2 legacy migration Train digest mismatch")
	}
	if v.Action != "mark_historical" && v.Action != "recover_integration" {
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
