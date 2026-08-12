package train

import (
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// RebindImplementationProofs invalidates all execution evidence after a
// server-owned replay. Rewritten commits cannot retain implementation proofs,
// accepted reviews, or gate evidence; the admitted tasks return to the
// unstarted queue and must be executed and reviewed again.
func RebindImplementationProofs(current model.TrainV2, mapping map[string]string, now time.Time) (model.TrainV2, error) {
	if err := model.ValidateTrainV2(current); err != nil {
		return model.TrainV2{}, err
	}
	if now.IsZero() || len(mapping) == 0 {
		return model.TrainV2{}, fmt.Errorf("invalid Train reconciliation mapping")
	}
	updated := current
	for i := range updated.Items {
		if updated.Items[i].Proof != nil {
			old := updated.Items[i].Proof.ImplementationSHA
			if mapped, ok := mapping[old]; !ok || model.ValidateCommitSHA(mapped) != nil {
				return model.TrainV2{}, fmt.Errorf("missing reconciliation mapping for item %s", updated.Items[i].TaskID)
			}
		}
		updated.Items[i].Status = model.TrainV2ItemQueued
		updated.Items[i].RunID = ""
		updated.Items[i].AgentID = ""
		updated.Items[i].StartHead = ""
		updated.Items[i].Proof = nil
		updated.Items[i].Review = nil
	}
	updated.FullProof = nil
	updated.Status = model.TrainV2Planned
	updated.Revision++
	updated.UpdatedAt = now
	if err := model.ValidateTrainV2(updated); err != nil {
		return model.TrainV2{}, err
	}
	return updated, nil
}

// ResetImplementationProofsForRestart invalidates all execution evidence
// after a server-owned reconciliation is discarded.  The lane is restarted
// from the refreshed target, so the admitted Tasks must execute again there;
// they must never be run on top of the discarded replay.
func ResetImplementationProofsForRestart(current model.TrainV2, now time.Time) (model.TrainV2, error) {
	if err := model.ValidateTrainV2(current); err != nil {
		return model.TrainV2{}, err
	}
	if now.IsZero() {
		return model.TrainV2{}, fmt.Errorf("invalid Train reconciliation restart time")
	}
	updated := current
	for i := range updated.Items {
		updated.Items[i].Status = model.TrainV2ItemQueued
		updated.Items[i].RunID = ""
		updated.Items[i].AgentID = ""
		updated.Items[i].StartHead = ""
		updated.Items[i].Proof = nil
		updated.Items[i].Review = nil
	}
	updated.FullProof = nil
	updated.Status = model.TrainV2Planned
	updated.Revision++
	updated.UpdatedAt = now
	if err := model.ValidateTrainV2(updated); err != nil {
		return model.TrainV2{}, err
	}
	return updated, nil
}

type IntegrationPlan struct {
	Status         string `json:"status"`
	TrainID        string `json:"train_id"`
	TargetHead     string `json:"target_head"`
	LaneHead       string `json:"lane_head"`
	NextAction     string `json:"next_action"`
	Reconciliation bool   `json:"reconciliation"`
}

type IntegrationReceipt struct {
	SchemaVersion   int       `json:"schema_version"`
	ProjectID       string    `json:"project_id"`
	TrainID         string    `json:"train_id"`
	BaseRevision    string    `json:"base_revision"`
	LaneHead        string    `json:"lane_head"`
	TargetBefore    string    `json:"target_before"`
	IntegrationHead string    `json:"integration_head"`
	RuntimeHead     string    `json:"runtime_head"`
	ProofCandidate  string    `json:"proof_candidate"`
	PreActivation   string    `json:"pre_activation"`
	PreSmoke        string    `json:"pre_smoke"`
	PostActivation  string    `json:"post_activation"`
	PostSmoke       string    `json:"post_smoke"`
	Status          string    `json:"status"`
	NextAction      string    `json:"next_action"`
	Conflict        string    `json:"conflict,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func ValidateIntegrationReceipt(v IntegrationReceipt) error {
	if v.SchemaVersion != 1 || model.ValidateProjectIdentifier(v.ProjectID) != nil {
		return fmt.Errorf("invalid Train integration receipt identity")
	}
	if _, _, err := model.ParseTrainV2ID(v.TrainID); err != nil {
		return err
	}
	for name, value := range map[string]string{"base_revision": v.BaseRevision, "lane_head": v.LaneHead, "target_before": v.TargetBefore, "integration_head": v.IntegrationHead, "runtime_head": v.RuntimeHead, "proof_candidate": v.ProofCandidate} {
		if value != "" {
			if err := model.ValidateCommitSHA(value); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	if v.Status == "" || v.NextAction == "" || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid Train integration receipt state")
	}
	if v.Status == "completed" {
		for name, value := range map[string]string{"base_revision": v.BaseRevision, "lane_head": v.LaneHead, "target_before": v.TargetBefore, "integration_head": v.IntegrationHead, "runtime_head": v.RuntimeHead, "proof_candidate": v.ProofCandidate} {
			if value == "" {
				return fmt.Errorf("completed receipt missing %s", name)
			}
		}
		for name, value := range map[string]string{"pre_activation": v.PreActivation, "pre_smoke": v.PreSmoke, "post_activation": v.PostActivation, "post_smoke": v.PostSmoke} {
			if value == "" {
				return fmt.Errorf("completed receipt missing %s", name)
			}
		}
	}
	return nil
}

// RecordFullProof binds the broad integration gate receipt to the exact
// proved lane head. Per-item scoped gates are not sufficient for this proof.
func RecordFullProof(current model.TrainV2, candidateHead string, gates []model.CompletionGateResult, now time.Time) (model.TrainV2, error) {
	if err := model.ValidateTrainV2(current); err != nil {
		return model.TrainV2{}, err
	}
	if now.IsZero() || len(gates) == 0 || model.ValidateCommitSHA(candidateHead) != nil {
		return model.TrainV2{}, fmt.Errorf("invalid train-wide proof input")
	}
	if current.Status == model.TrainV2Blocked || current.Status == model.TrainV2Paused {
		return model.TrainV2{}, fmt.Errorf("blocked or paused Train cannot be proved")
	}
	var laneHead string
	for _, item := range current.Items {
		if item.Status != model.TrainV2ItemReviewed || item.Proof == nil || item.Review == nil || item.Review.Outcome != model.ReviewOutcomeAccepted {
			return model.TrainV2{}, fmt.Errorf("Train item %q is not fully reviewed and proved", item.TaskID)
		}
		if item.Proof.ImplementationSHA == "" {
			return model.TrainV2{}, fmt.Errorf("Train item %q has no implementation head", item.TaskID)
		}
		laneHead = item.Proof.ImplementationSHA
	}
	if laneHead != candidateHead {
		return model.TrainV2{}, fmt.Errorf("full proof candidate is not the exact lane head")
	}
	if err := model.ValidateServerGateEvidence(gates); err != nil {
		return model.TrainV2{}, err
	}
	updated := current
	updated.FullProof = &model.TrainV2FullProof{CandidateHead: candidateHead, GateResults: append([]model.CompletionGateResult{}, gates...), RecordedAt: now}
	updated.Status = model.TrainV2ReadyForIntegration
	updated.Revision++
	updated.UpdatedAt = now
	if err := model.ValidateTrainV2(updated); err != nil {
		return model.TrainV2{}, err
	}
	return updated, nil
}

func PlanIntegration(current model.TrainV2, targetHead string, targetIsAncestor bool) (IntegrationPlan, error) {
	if err := model.ValidateTrainV2(current); err != nil {
		return IntegrationPlan{}, err
	}
	if current.FullProof == nil || current.Status != model.TrainV2ReadyForIntegration {
		return IntegrationPlan{}, fmt.Errorf("Train is not ready for integration")
	}
	if err := model.ValidateCommitSHA(targetHead); err != nil {
		return IntegrationPlan{}, err
	}
	laneHead := current.FullProof.CandidateHead
	plan := IntegrationPlan{
		Status:     "fast_forward_required",
		TrainID:    current.ID,
		TargetHead: targetHead,
		LaneHead:   laneHead,
		NextAction: "activate_pre_merge_and_fast_forward",
	}
	if targetHead == laneHead {
		plan.Status, plan.NextAction = "already_integrated", "activate_post_merge_and_record_receipt"
		return plan, nil
	}
	if !targetIsAncestor {
		plan.Status, plan.NextAction, plan.Reconciliation = "reconciliation_required", "create_train_reconciliation_receipt", true
	}
	return plan, nil
}

func MarkIntegrated(current model.TrainV2, integrationHead, runtimeHead string, now time.Time) (model.TrainV2, error) {
	if err := model.ValidateTrainV2(current); err != nil {
		return model.TrainV2{}, err
	}
	if current.FullProof == nil || current.Status != model.TrainV2ReadyForIntegration || model.ValidateCommitSHA(integrationHead) != nil || model.ValidateCommitSHA(runtimeHead) != nil || integrationHead != current.FullProof.CandidateHead || now.IsZero() {
		return model.TrainV2{}, fmt.Errorf("invalid Train integration completion")
	}
	updated := current
	updated.Status = model.TrainV2Completed
	updated.Revision++
	updated.UpdatedAt = now
	if err := model.ValidateTrainV2(updated); err != nil {
		return model.TrainV2{}, err
	}
	return updated, nil
}
