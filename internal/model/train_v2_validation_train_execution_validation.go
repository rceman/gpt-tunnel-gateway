package model

import (
	"fmt"
	"strings"
)

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
