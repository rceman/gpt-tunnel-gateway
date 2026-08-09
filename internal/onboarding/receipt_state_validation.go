package onboarding

import (
	"errors"
	"fmt"
)

// ValidateHubCommittedReceiptIntrinsic validates the hub-committed receipt
// before local activation begins.
func ValidateHubCommittedReceiptIntrinsic(receipt Receipt) error {
	return validatePostHubReceiptIntrinsic(receipt, StateHubCommitted)
}

// ValidateActivatedReceiptIntrinsic validates the durable receipt after the
// local registry, mirror and readiness proofs have completed.
func ValidateActivatedReceiptIntrinsic(receipt Receipt) error {
	return validatePostHubReceiptIntrinsic(receipt, StateActivated)
}

func validatePostHubReceiptIntrinsic(receipt Receipt, expectedState ReceiptState) error {
	if receipt.State != expectedState {
		return fmt.Errorf("invalid %s receipt state %q", expectedState, receipt.State)
	}
	if receipt.SchemaVersion != PositiveInteger(1) {
		return errors.New("receipt schema_version must be 1")
	}
	if !receiptUUIDPattern.MatchString(receipt.OperationID) {
		return errors.New("receipt operation_id must be a lowercase UUID")
	}
	if !receiptSHA256Pattern.MatchString(receipt.RequestSHA256) {
		return errors.New("receipt request_sha256 must be a lowercase SHA-256 digest")
	}
	if err := validateProjectID(receipt.ProjectID, "receipt project_id"); err != nil {
		return fmt.Errorf("receipt project_id: %w", err)
	}
	proof := receipt.RepositoryProof
	if err := validateAbsolutePath(proof.Root, "receipt repository_proof.root"); err != nil {
		return fmt.Errorf("receipt repository_proof.root: %w", err)
	}
	if err := validateRemote(proof.Remote, "receipt repository_proof.remote"); err != nil {
		return fmt.Errorf("receipt repository_proof.remote: %w", err)
	}
	if err := validateRepositoryURL(proof.RepositoryURL, "receipt repository_proof.repository_url"); err != nil {
		return fmt.Errorf("receipt repository_proof.repository_url: %w", err)
	}
	if err := validateBranch(proof.DefaultBranch, "receipt repository_proof.default_branch"); err != nil {
		return fmt.Errorf("receipt repository_proof.default_branch: %w", err)
	}
	if err := validateBranch(proof.Branch, "receipt repository_proof.branch"); err != nil {
		return fmt.Errorf("receipt repository_proof.branch: %w", err)
	}
	if proof.Branch != proof.DefaultBranch {
		return errors.New("receipt repository_proof.branch must equal default_branch")
	}
	if err := validateSHA40(proof.Head, "receipt repository_proof.head"); err != nil {
		return fmt.Errorf("receipt repository_proof.head: %w", err)
	}
	if err := validateAbsolutePath(proof.GatewayStateDir, "receipt repository_proof.gateway_state_dir"); err != nil {
		return fmt.Errorf("receipt repository_proof.gateway_state_dir: %w", err)
	}
	if !receipt.WorktreeProof.Clean {
		return errors.New("receipt worktree_proof.clean must be true")
	}
	if err := validateSHA256(receipt.WorktreeProof.StatusSHA256, "worktree_proof.status_sha256"); err != nil {
		return err
	}
	if err := validateSessionProofIntrinsic(receipt.SessionProof); err != nil {
		return err
	}
	if err := validateRegistryDigests(receipt.RegistryDigests); err != nil {
		return err
	}
	if err := validateSHA40(receipt.Hub.Before, "receipt hub.before"); err != nil {
		return fmt.Errorf("receipt hub.before: %w", err)
	}
	if receipt.Hub.After == nil {
		return errors.New("hub-committed receipt requires hub.after")
	}
	if err := validateSHA40(*receipt.Hub.After, "receipt hub.after"); err != nil {
		return fmt.Errorf("receipt hub.after: %w", err)
	}
	if *receipt.Hub.After == receipt.Hub.Before {
		return errors.New("hub-committed receipt hub.after must differ from hub.before")
	}
	if err := validatePreparedHubPaths(receipt.Hub.Paths, receipt.ProjectID); err != nil {
		return err
	}
	if expectedState == StateHubCommitted {
		if receipt.MirrorProof != nil {
			return errors.New("hub-committed receipt must not contain mirror_proof")
		}
	} else if expectedState == StateActivated {
		if err := validateMirrorProof(receipt.MirrorProof); err != nil {
			return err
		}
	} else if expectedState == StateRecoveryRequired && receipt.MirrorProof != nil {
		if err := validateMirrorProof(receipt.MirrorProof); err != nil {
			return err
		}
	} else if expectedState != StateRecoveryRequired {
		return fmt.Errorf("unsupported post-hub receipt state %q", expectedState)
	}
	if err := validateCreatedProject(receipt.CreatedProject, receipt.ProjectID, receipt.RepositoryProof); err != nil {
		return err
	}
	if err := validateCreatedPlan(receipt.CreatedPlan, receipt.ProjectID); err != nil {
		return err
	}
	if err := validateCreatedIdentifiers(receipt.CreatedIdentifiers, receipt.ProjectID); err != nil {
		return err
	}
	if expectedState == StateHubCommitted {
		if err := validateCommittedRecovery(receipt.Recovery); err != nil {
			return err
		}
	} else if expectedState == StateRecoveryRequired {
		if err := validateRecoveryRequired(receipt.Recovery); err != nil {
			return err
		}
	} else if expectedState == StateActivated {
		if err := validateActivatedRecovery(receipt.Recovery); err != nil {
			return err
		}
	}
	if expectedState == StateRecoveryRequired {
		if err := validateRecoveryEvidence(receipt); err != nil {
			return err
		}
	}
	if expectedState == StateHubCommitted {
		if err := validateCommittedTimestamps(receipt.Timestamps); err != nil {
			return err
		}
	} else if expectedState == StateRecoveryRequired {
		if err := validateRecoveryTimestamps(receipt.Timestamps); err != nil {
			return err
		}
	} else if expectedState == StateActivated {
		if err := validateActivatedTimestamps(receipt.Timestamps); err != nil {
			return err
		}
	}
	return nil
}

func validateMirrorProof(proof *MirrorProof) error {
	if proof == nil {
		return errors.New("activated receipt requires mirror_proof")
	}
	if err := validateAbsolutePath(proof.Path, "mirror_proof.path"); err != nil {
		return err
	}
	if err := validateRepositoryURL(proof.RepositoryURL, "mirror_proof.repository_url"); err != nil {
		return err
	}
	return validateSHA40(proof.Head, "mirror_proof.head")
}

func validateCreatedProject(created *CreatedProject, projectID string, proof RepositoryProof) error {
	if created == nil {
		return errors.New("hub-committed receipt requires created_project")
	}
	if created.ProjectID != projectID || created.RepositoryURL != proof.RepositoryURL || created.DefaultBranch != proof.DefaultBranch {
		return errors.New("created_project does not match repository proof")
	}
	if err := validateProjectID(created.ProjectID, "created_project.project_id"); err != nil {
		return err
	}
	if err := validateRepositoryURL(created.RepositoryURL, "created_project.repository_url"); err != nil {
		return err
	}
	if err := validateBranch(created.DefaultBranch, "created_project.default_branch"); err != nil {
		return err
	}
	if created.Status != "active" {
		return errors.New("created_project.status must be active")
	}
	if (created.WorkflowRepository == nil) != (created.WorkflowCommit == nil) {
		return errors.New("created_project workflow fields must be provided together")
	}
	return nil
}

func validateCreatedPlan(created *CreatedPlan, projectID string) error {
	if created == nil {
		return errors.New("hub-committed receipt requires created_plan")
	}
	if created.SchemaVersion != PositiveInteger(2) || created.ProjectID != projectID || created.Revision < 1 {
		return errors.New("created_plan is invalid")
	}
	if created.Path != fmt.Sprintf("gpt-tunnel/v1/projects/%s/plan/current.json", projectID) {
		return errors.New("created_plan.path is not canonical")
	}
	return nil
}

func validateCreatedIdentifiers(created *CreatedIdentifiers, projectID string) error {
	if created == nil {
		return errors.New("hub-committed receipt requires created_identifiers")
	}
	if created.SchemaVersion != PositiveInteger(1) || created.ProjectID != projectID {
		return errors.New("created_identifiers identity is invalid")
	}
	if err := validateProjectCode(created.ProjectCode, "created_identifiers.project_code"); err != nil {
		return err
	}
	if created.NextTaskNumber != PositiveInteger(1) || created.NextADRNumber != PositiveInteger(1) {
		return errors.New("created_identifiers counters must both equal 1")
	}
	return nil
}

func validateCommittedTimestamps(timestamps Timestamps) error {
	if timestamps.PreparedAt == nil || timestamps.HubCommittedAt == nil || timestamps.ActivatedAt != nil || timestamps.RolledBackAt != nil {
		return errors.New("hub-committed receipt timestamps are invalid")
	}
	started, err := parseReceiptTime(timestamps.StartedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.started_at: %w", err)
	}
	prepared, err := parseReceiptTime(*timestamps.PreparedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.prepared_at: %w", err)
	}
	committed, err := parseReceiptTime(*timestamps.HubCommittedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.hub_committed_at: %w", err)
	}
	updated, err := parseReceiptTime(timestamps.UpdatedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.updated_at: %w", err)
	}
	if !started.Before(prepared) || prepared.After(committed) || committed.After(updated) {
		return errors.New("hub-committed receipt timestamp order is invalid")
	}
	return nil
}

func validateCommittedRecovery(recovery Recovery) error {
	if recovery.Status != RecoveryNotRequired || recovery.LastCompletedState != nil || recovery.LastDurableStep != nil || recovery.Reason != nil || recovery.SafeCorrectiveAction != nil || recovery.RolledBackAt != nil || recovery.RollbackProof != nil {
		return errors.New("hub-committed receipt recovery must be not_required without later fields")
	}
	return nil
}
