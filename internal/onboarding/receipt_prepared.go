package onboarding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ValidatePreparedReceiptIntrinsic validates every prepared Receipt invariant
// that does not depend on an onboarding Request. It is shared by the journal
// loader and the Request-bound validator.
func ValidatePreparedReceiptIntrinsic(receipt Receipt) error {
	switch receipt.State {
	case StatePrepared:
	case StateHubCommitted, StateActivated, StateRecoveryRequired, StateRolledBack:
		return fmt.Errorf("unsupported receipt state %q in O3a2", receipt.State)
	default:
		return fmt.Errorf("invalid receipt state %q", receipt.State)
	}
	if receipt.SchemaVersion != PositiveInteger(1) {
		return fmt.Errorf("receipt schema_version must be 1")
	}
	if !receiptUUIDPattern.MatchString(receipt.OperationID) {
		return fmt.Errorf("receipt operation_id must be a lowercase UUID")
	}
	if !receiptSHA256Pattern.MatchString(receipt.RequestSHA256) {
		return fmt.Errorf("receipt request_sha256 must be a lowercase SHA-256 digest")
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
	if receipt.Hub.After != nil {
		return errors.New("prepared receipt must not contain hub.after")
	}
	if err := validatePreparedHubPaths(receipt.Hub.Paths, receipt.ProjectID); err != nil {
		return err
	}
	if receipt.CreatedProject != nil || receipt.CreatedPlan != nil || receipt.CreatedIdentifiers != nil || receipt.MirrorProof != nil {
		return errors.New("prepared receipt must not contain created records or mirror_proof")
	}
	if err := validatePreparedTimestamps(receipt.Timestamps); err != nil {
		return err
	}
	if err := validatePreparedRecovery(receipt.Recovery); err != nil {
		return err
	}
	return nil
}

// ValidatePreparedReceipt validates the O3a2 prepared receipt and binds it to
// the already validated onboarding request.
func ValidatePreparedReceipt(receipt Receipt, request Request) error {
	if err := ValidateRequest(request); err != nil {
		return fmt.Errorf("invalid onboarding request: %w", err)
	}
	if err := ValidatePreparedReceiptIntrinsic(receipt); err != nil {
		return err
	}
	expectedRequestDigest, err := RequestDigest(request)
	if err != nil {
		return fmt.Errorf("compute request digest: %w", err)
	}
	if receipt.RequestSHA256 != expectedRequestDigest {
		return fmt.Errorf("receipt request_sha256 does not match request")
	}
	if receipt.ProjectID != request.ProjectID {
		return fmt.Errorf("receipt project_id does not match request")
	}
	proof := receipt.RepositoryProof
	if proof.Root != request.Root {
		return fmt.Errorf("receipt repository_proof.root does not match request")
	}
	if proof.Remote != request.Remote {
		return fmt.Errorf("receipt repository_proof.remote does not match request")
	}
	if proof.RepositoryURL != request.RepositoryURL {
		return fmt.Errorf("receipt repository_proof.repository_url does not match request")
	}
	if proof.DefaultBranch != request.DefaultBranch || proof.Branch != request.DefaultBranch {
		return fmt.Errorf("receipt repository branch does not match request default branch")
	}
	if proof.GatewayStateDir != request.GatewayStateDir {
		return fmt.Errorf("receipt repository_proof.gateway_state_dir does not match request")
	}
	if err := validateSessionProof(receipt.SessionProof, request.Airelay); err != nil {
		return err
	}
	if receipt.Hub.Before != request.ExpectedHubRevision {
		return fmt.Errorf("receipt hub.before does not match request expected hub revision")
	}
	return nil
}

func validateSHA256(value, field string) error {
	if !receiptSHA256Pattern.MatchString(value) {
		return fmt.Errorf("receipt %s must be a lowercase SHA-256 digest", field)
	}
	return nil
}

func validateSessionProof(proof SessionProof, airelay Airelay) error {
	if err := validateSessionProofIntrinsic(proof); err != nil {
		return err
	}
	if proof.Required != airelay.SessionRequired {
		return errors.New("receipt session_proof.required does not match request")
	}
	if proof.Required {
		if airelay.SessionKey == nil {
			return errors.New("request requires an Airelay session key")
		}
		if proof.SessionKey == nil || *proof.SessionKey != *airelay.SessionKey {
			return errors.New("receipt session_proof.session_key does not match request")
		}
		return nil
	}
	return nil
}

func validateSessionProofIntrinsic(proof SessionProof) error {
	if proof.Required {
		if proof.SessionKey == nil {
			return errors.New("required receipt session needs a session key")
		}
		if err := validateSessionKey(*proof.SessionKey, "receipt session_proof.session_key"); err != nil {
			return fmt.Errorf("receipt session_proof.session_key: %w", err)
		}
		if proof.Status != "active" {
			return errors.New("required receipt session must be active")
		}
		if proof.ControllerProtocolVersion == nil {
			return errors.New("required receipt session needs a positive controller protocol version")
		}
		return validatePositiveInteger(*proof.ControllerProtocolVersion, "receipt session_proof.controller_protocol_version")
	}
	if proof.Status != "not_required" {
		return errors.New("optional receipt session must have status not_required")
	}
	if proof.SessionKey != nil || proof.ControllerProtocolVersion != nil {
		return errors.New("optional receipt session must not contain key or protocol version")
	}
	return nil
}

func validateRegistryDigests(digests RegistryDigests) error {
	for field, value := range map[string]string{
		"registry_digests.managed_before_sha256": digests.ManagedBeforeSHA256,
		"registry_digests.managed_after_sha256":  digests.ManagedAfterSHA256,
		"registry_digests.project_sha256":        digests.ProjectSHA256,
		"registry_digests.plan_sha256":           digests.PlanSHA256,
		"registry_digests.identifiers_sha256":    digests.IdentifiersSHA256,
	} {
		if err := validateSHA256(value, field); err != nil {
			return err
		}
	}
	if digests.ManagedBeforeSHA256 == digests.ManagedAfterSHA256 {
		return errors.New("registry managed_before_sha256 and managed_after_sha256 must differ")
	}
	return nil
}

func validatePreparedHubPaths(paths []string, projectID string) error {
	if len(paths) != len(preparedReceiptPaths) {
		return errors.New("receipt hub.paths must contain exactly three paths")
	}
	expected := make([]string, len(preparedReceiptPaths))
	for i, pattern := range preparedReceiptPaths {
		expected[i] = fmt.Sprintf(pattern, projectID)
	}
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if !receiptRelativePathPattern.MatchString(path) || strings.HasPrefix(path, "/") || strings.Contains(path, "//") || strings.Contains(path, "../") || strings.Contains(path, "/./") {
			return fmt.Errorf("receipt hub path %q is not a canonical relative path", path)
		}
		if _, ok := seen[path]; ok {
			return fmt.Errorf("receipt hub.paths contains duplicate %q", path)
		}
		seen[path] = struct{}{}
	}
	for _, path := range expected {
		if _, ok := seen[path]; !ok {
			return fmt.Errorf("receipt hub.paths missing canonical path %q", path)
		}
	}
	return nil
}

func validatePreparedTimestamps(timestamps Timestamps) error {
	if timestamps.PreparedAt == nil {
		return errors.New("prepared receipt requires timestamps.prepared_at")
	}
	if timestamps.HubCommittedAt != nil || timestamps.ActivatedAt != nil || timestamps.RolledBackAt != nil {
		return errors.New("prepared receipt must not contain later timestamps")
	}
	started, err := parseReceiptTime(timestamps.StartedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.started_at: %w", err)
	}
	prepared, err := parseReceiptTime(*timestamps.PreparedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.prepared_at: %w", err)
	}
	updated, err := parseReceiptTime(timestamps.UpdatedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.updated_at: %w", err)
	}
	if !started.Before(prepared) {
		return errors.New("receipt timestamps.started_at must be before prepared_at")
	}
	if prepared.After(updated) {
		return errors.New("receipt timestamps.prepared_at must not be after updated_at")
	}
	return nil
}

func parseReceiptTime(value string) (time.Time, error) {
	if err := validateDateTime(value, "receipt timestamp"); err != nil {
		return time.Time{}, err
	}
	if len(value) > 10 && value[10] == 't' {
		value = value[:10] + "T" + value[11:]
	}
	return time.Parse(time.RFC3339Nano, value)
}

func validatePreparedRecovery(recovery Recovery) error {
	if recovery.Status != "not_required" {
		return errors.New("prepared receipt recovery.status must be not_required")
	}
	if recovery.LastCompletedState != nil || recovery.Reason != nil || recovery.RolledBackAt != nil || recovery.RollbackProof != nil {
		return errors.New("prepared receipt recovery must not contain later recovery fields")
	}
	return nil
}

// CanonicalPreparedReceiptJSON validates and serializes a prepared receipt in
// compact JSON form. It does not mutate the receipt or request.
func CanonicalPreparedReceiptJSON(receipt Receipt, request Request) ([]byte, error) {
	if err := ValidatePreparedReceipt(receipt, request); err != nil {
		return nil, err
	}
	return json.Marshal(receipt)
}

// PreparedReceiptDigest returns the lower-case SHA-256 digest of the canonical
// prepared receipt JSON.
func PreparedReceiptDigest(receipt Receipt, request Request) (string, error) {
	data, err := CanonicalPreparedReceiptJSON(receipt, request)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
