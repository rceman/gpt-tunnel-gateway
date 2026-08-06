package onboarding

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// ReceiptState identifies the durable phase represented by a prepared
// onboarding receipt. The later states are modelled here so receipts remain
// forward-readable, but O3a2 only accepts the prepared state.
type ReceiptState string

const (
	StatePrepared         ReceiptState = "prepared"
	StateHubCommitted     ReceiptState = "hub_committed"
	StateActivated        ReceiptState = "activated"
	StateRecoveryRequired ReceiptState = "recovery_required"
	StateRolledBack       ReceiptState = "rolled_back"
)

type Receipt struct {
	SchemaVersion      PositiveInteger     `json:"schema_version"`
	OperationID        string              `json:"operation_id"`
	RequestSHA256      string              `json:"request_sha256"`
	State              ReceiptState        `json:"state"`
	ProjectID          string              `json:"project_id"`
	RepositoryProof    RepositoryProof     `json:"repository_proof"`
	WorktreeProof      WorktreeProof       `json:"worktree_proof"`
	SessionProof       SessionProof        `json:"session_proof"`
	RegistryDigests    RegistryDigests     `json:"registry_digests"`
	Hub                HubProof            `json:"hub"`
	CreatedProject     *CreatedProject     `json:"created_project,omitempty"`
	CreatedPlan        *CreatedPlan        `json:"created_plan,omitempty"`
	CreatedIdentifiers *CreatedIdentifiers `json:"created_identifiers,omitempty"`
	MirrorProof        *MirrorProof        `json:"mirror_proof,omitempty"`
	Timestamps         Timestamps          `json:"timestamps"`
	Recovery           Recovery            `json:"recovery"`
}

type RepositoryProof struct {
	Root            string `json:"root"`
	Remote          string `json:"remote"`
	RepositoryURL   string `json:"repository_url"`
	DefaultBranch   string `json:"default_branch"`
	Branch          string `json:"branch"`
	Head            string `json:"head"`
	GatewayStateDir string `json:"gateway_state_dir"`
}

type WorktreeProof struct {
	Clean        bool   `json:"clean"`
	StatusSHA256 string `json:"status_sha256"`
}

type SessionProof struct {
	Required                  bool             `json:"required"`
	SessionKey                *string          `json:"session_key,omitempty"`
	Status                    string           `json:"status"`
	ControllerProtocolVersion *PositiveInteger `json:"controller_protocol_version,omitempty"`
}

type RegistryDigests struct {
	ManagedBeforeSHA256 string `json:"managed_before_sha256"`
	ManagedAfterSHA256  string `json:"managed_after_sha256"`
	ProjectSHA256       string `json:"project_sha256"`
	PlanSHA256          string `json:"plan_sha256"`
	IdentifiersSHA256   string `json:"identifiers_sha256"`
}

type HubProof struct {
	Before string   `json:"before"`
	After  *string  `json:"after,omitempty"`
	Paths  []string `json:"paths"`
}

type CreatedProject struct {
	ProjectID          string  `json:"project_id"`
	RepositoryURL      string  `json:"repository_url"`
	DefaultBranch      string  `json:"default_branch"`
	Status             string  `json:"status"`
	WorkflowRepository *string `json:"workflow_repository,omitempty"`
	WorkflowCommit     *string `json:"workflow_commit,omitempty"`
}

type CreatedPlan struct {
	SchemaVersion PositiveInteger `json:"schema_version"`
	ProjectID     string          `json:"project_id"`
	Revision      PositiveInteger `json:"revision"`
	Path          string          `json:"path"`
}

type CreatedIdentifiers struct {
	SchemaVersion  PositiveInteger `json:"schema_version"`
	ProjectID      string          `json:"project_id"`
	ProjectCode    string          `json:"project_code"`
	NextTaskNumber PositiveInteger `json:"next_task_number"`
	NextADRNumber  PositiveInteger `json:"next_adr_number"`
}

type MirrorProof struct {
	Path          string `json:"path"`
	RepositoryURL string `json:"repository_url"`
	Head          string `json:"head"`
}

type Timestamps struct {
	StartedAt      string  `json:"started_at"`
	UpdatedAt      string  `json:"updated_at"`
	PreparedAt     *string `json:"prepared_at,omitempty"`
	HubCommittedAt *string `json:"hub_committed_at,omitempty"`
	ActivatedAt    *string `json:"activated_at,omitempty"`
	RolledBackAt   *string `json:"rolled_back_at,omitempty"`
}

type ReceiptTimestamps = Timestamps

type Recovery struct {
	Status             string         `json:"status"`
	LastCompletedState *ReceiptState  `json:"last_completed_state,omitempty"`
	Reason             *string        `json:"reason,omitempty"`
	RolledBackAt       *string        `json:"rolled_back_at,omitempty"`
	RollbackProof      *RollbackProof `json:"rollback_proof,omitempty"`
}

type ReceiptRecovery = Recovery

type RollbackProof struct {
	ManagedAfterSHA256 string    `json:"managed_after_sha256"`
	HubRevision        *string   `json:"hub_revision,omitempty"`
	HubPaths           *[]string `json:"hub_paths,omitempty"`
}

var (
	receiptUUIDPattern         = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	receiptSHA256Pattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	receiptRelativePathPattern = regexp.MustCompile(`^[^/][^\x00]*$`)
)

var preparedReceiptPaths = []string{
	"gpt-tunnel/v1/projects/%s/project.json",
	"gpt-tunnel/v1/projects/%s/plan/current.json",
	"gpt-tunnel/v1/projects/%s/identifiers.json",
}

// DecodeReceipt strictly decodes a receipt object. It validates JSON syntax,
// duplicate keys, null values, trailing data, and unknown fields before type
// decoding. State-specific validation is performed by ValidatePreparedReceipt.
func DecodeReceipt(data []byte) (Receipt, error) {
	var receipt Receipt
	if err := decodeReceiptStrict(data, &receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func decodeReceiptStrict(data []byte, destination any) error {
	if !utf8.Valid(data) {
		return errors.New("receipt JSON is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder, true); err != nil {
		return fmt.Errorf("invalid receipt JSON: %w", err)
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("invalid receipt JSON: trailing token %v", token)
		}
		return fmt.Errorf("invalid receipt JSON: trailing data: %w", err)
	}
	if err := requireReceiptFields(data); err != nil {
		return err
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid receipt: %w", err)
	}
	return nil
}

func requireReceiptFields(data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return fmt.Errorf("invalid receipt object: %w", err)
	}
	if object == nil {
		return errors.New("receipt must be a JSON object")
	}
	if err := requireReceiptObjectFields(object, "receipt", []string{
		"schema_version", "operation_id", "request_sha256", "state", "project_id",
		"repository_proof", "worktree_proof", "session_proof", "registry_digests",
		"hub", "timestamps", "recovery",
	}); err != nil {
		return err
	}
	if err := receiptRequireObject(object, "repository_proof"); err != nil {
		return err
	}
	if err := receiptRequireObject(object, "worktree_proof"); err != nil {
		return err
	}
	if err := receiptRequireObject(object, "session_proof"); err != nil {
		return err
	}
	if err := receiptRequireObject(object, "registry_digests"); err != nil {
		return err
	}
	if err := receiptRequireObject(object, "hub"); err != nil {
		return err
	}
	if err := receiptRequireObject(object, "timestamps"); err != nil {
		return err
	}
	if err := receiptRequireObject(object, "recovery"); err != nil {
		return err
	}

	if err := requireReceiptNestedFields(object["repository_proof"], "repository_proof", []string{
		"root", "remote", "repository_url", "default_branch", "branch", "head", "gateway_state_dir",
	}); err != nil {
		return err
	}
	if err := requireReceiptNestedFields(object["worktree_proof"], "worktree_proof", []string{"clean", "status_sha256"}); err != nil {
		return err
	}
	if err := requireReceiptNestedFields(object["session_proof"], "session_proof", []string{"required", "status"}); err != nil {
		return err
	}
	if err := requireReceiptNestedFields(object["registry_digests"], "registry_digests", []string{
		"managed_before_sha256", "managed_after_sha256", "project_sha256", "plan_sha256", "identifiers_sha256",
	}); err != nil {
		return err
	}
	if err := requireReceiptNestedFields(object["hub"], "hub", []string{"before", "paths"}); err != nil {
		return err
	}
	if err := requireReceiptNestedFields(object["timestamps"], "timestamps", []string{"started_at", "updated_at"}); err != nil {
		return err
	}
	if err := requireReceiptNestedFields(object["recovery"], "recovery", []string{"status"}); err != nil {
		return err
	}

	if raw, ok := object["created_project"]; ok {
		if err := requireReceiptNestedFields(raw, "created_project", []string{"project_id", "repository_url", "default_branch", "status"}); err != nil {
			return err
		}
		if err := requireReceiptPairedFields(raw, "created_project", "workflow_repository", "workflow_commit"); err != nil {
			return err
		}
	}
	if raw, ok := object["created_plan"]; ok {
		if err := requireReceiptNestedFields(raw, "created_plan", []string{"schema_version", "project_id", "revision", "path"}); err != nil {
			return err
		}
	}
	if raw, ok := object["created_identifiers"]; ok {
		if err := requireReceiptNestedFields(raw, "created_identifiers", []string{
			"schema_version", "project_id", "project_code", "next_task_number", "next_adr_number",
		}); err != nil {
			return err
		}
	}
	if raw, ok := object["mirror_proof"]; ok {
		if err := requireReceiptNestedFields(raw, "mirror_proof", []string{"path", "repository_url", "head"}); err != nil {
			return err
		}
	}
	if raw, ok := object["recovery"]; ok {
		var recovery map[string]json.RawMessage
		if err := decodeReceiptObject(raw, &recovery, "recovery"); err != nil {
			return err
		}
		if rollbackRaw, ok := recovery["rollback_proof"]; ok {
			if err := requireReceiptNestedFields(rollbackRaw, "recovery.rollback_proof", []string{"managed_after_sha256"}); err != nil {
				return err
			}
			if err := requireReceiptPairedFields(rollbackRaw, "recovery.rollback_proof", "hub_revision", "hub_paths"); err != nil {
				return err
			}
		}
	}
	return nil
}

func requireReceiptObjectFields(object map[string]json.RawMessage, name string, fields []string) error {
	for _, field := range fields {
		if _, ok := object[field]; !ok {
			return fmt.Errorf("receipt %s missing required field %q", name, field)
		}
	}
	return nil
}

func requireReceiptNestedFields(raw json.RawMessage, name string, fields []string) error {
	var object map[string]json.RawMessage
	if err := decodeReceiptObject(raw, &object, name); err != nil {
		return err
	}
	for _, field := range fields {
		if _, ok := object[field]; !ok {
			return fmt.Errorf("receipt %s missing required field %q", name, field)
		}
	}
	return nil
}

func receiptRequireObject(object map[string]json.RawMessage, field string) error {
	raw, ok := object[field]
	if !ok {
		return fmt.Errorf("receipt missing required field %q", field)
	}
	var nested map[string]json.RawMessage
	return decodeReceiptObject(raw, &nested, field)
}

func decodeReceiptObject(raw json.RawMessage, destination *map[string]json.RawMessage, name string) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("receipt %s must be an object", name)
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return fmt.Errorf("receipt %s must be an object: %w", name, err)
	}
	if *destination == nil {
		return fmt.Errorf("receipt %s must be an object", name)
	}
	return nil
}

func requireReceiptPairedFields(raw json.RawMessage, name, first, second string) error {
	var object map[string]json.RawMessage
	if err := decodeReceiptObject(raw, &object, name); err != nil {
		return err
	}
	_, firstPresent := object[first]
	_, secondPresent := object[second]
	if firstPresent != secondPresent {
		return fmt.Errorf("receipt %s fields %q and %q must be provided together", name, first, second)
	}
	return nil
}

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
	} else if err := validateMirrorProof(receipt.MirrorProof); err != nil {
		return err
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
		if err := validateCommittedTimestamps(receipt.Timestamps); err != nil {
			return err
		}
		if err := validateCommittedRecovery(receipt.Recovery); err != nil {
			return err
		}
	} else {
		if err := validateActivatedTimestamps(receipt.Timestamps); err != nil {
			return err
		}
		if err := validateActivatedRecovery(receipt.Recovery); err != nil {
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
	if recovery.Status != "not_required" || recovery.LastCompletedState != nil || recovery.Reason != nil || recovery.RolledBackAt != nil || recovery.RollbackProof != nil {
		return errors.New("hub-committed receipt recovery must be not_required without later fields")
	}
	return nil
}

func validateActivatedTimestamps(timestamps Timestamps) error {
	if timestamps.PreparedAt == nil || timestamps.HubCommittedAt == nil || timestamps.ActivatedAt == nil || timestamps.RolledBackAt != nil {
		return errors.New("activated receipt timestamps are invalid")
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
	activated, err := parseReceiptTime(*timestamps.ActivatedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.activated_at: %w", err)
	}
	updated, err := parseReceiptTime(timestamps.UpdatedAt)
	if err != nil {
		return fmt.Errorf("receipt timestamps.updated_at: %w", err)
	}
	if !started.Before(prepared) || prepared.After(committed) || committed.After(activated) || activated.After(updated) {
		return errors.New("activated receipt timestamp order is invalid")
	}
	return nil
}

func validateActivatedRecovery(recovery Recovery) error {
	return validateCommittedRecovery(recovery)
}

func ValidateHubCommittedReceipt(receipt Receipt, request Request) error {
	if err := ValidateRequest(request); err != nil {
		return fmt.Errorf("invalid onboarding request: %w", err)
	}
	if err := ValidateHubCommittedReceiptIntrinsic(receipt); err != nil {
		return err
	}
	if err := validateReceiptRequestBinding(receipt, request); err != nil {
		return err
	}
	if receipt.CreatedProject.WorkflowRepository != nil {
		if request.Workflow == nil || *receipt.CreatedProject.WorkflowRepository != request.Workflow.Repository || receipt.CreatedProject.WorkflowCommit == nil || *receipt.CreatedProject.WorkflowCommit != request.Workflow.Commit {
			return errors.New("created_project workflow does not match request")
		}
	} else if request.Workflow != nil {
		return errors.New("created_project workflow is missing")
	}
	if receipt.CreatedPlan.Revision != request.InitialPlan.Revision || receipt.CreatedPlan.ProjectID != request.InitialPlan.ProjectID {
		return errors.New("created_plan does not match request initial plan")
	}
	if receipt.CreatedIdentifiers.ProjectCode != request.ProjectCode {
		return errors.New("created_identifiers project code does not match request")
	}
	return nil
}

func ValidateActivatedReceipt(receipt Receipt, request Request) error {
	if err := ValidateRequest(request); err != nil {
		return fmt.Errorf("invalid onboarding request: %w", err)
	}
	if err := ValidateActivatedReceiptIntrinsic(receipt); err != nil {
		return err
	}
	if err := validateReceiptRequestBinding(receipt, request); err != nil {
		return err
	}
	if receipt.CreatedProject.WorkflowRepository != nil {
		if request.Workflow == nil || *receipt.CreatedProject.WorkflowRepository != request.Workflow.Repository || receipt.CreatedProject.WorkflowCommit == nil || *receipt.CreatedProject.WorkflowCommit != request.Workflow.Commit {
			return errors.New("created_project workflow does not match request")
		}
	} else if request.Workflow != nil {
		return errors.New("created_project workflow is missing")
	}
	if receipt.CreatedPlan.Revision != request.InitialPlan.Revision || receipt.CreatedPlan.ProjectID != request.InitialPlan.ProjectID {
		return errors.New("created_plan does not match request initial plan")
	}
	if receipt.CreatedIdentifiers.ProjectCode != request.ProjectCode {
		return errors.New("created_identifiers project code does not match request")
	}
	if receipt.MirrorProof.RepositoryURL != request.RepositoryURL {
		return errors.New("mirror_proof repository URL does not match request")
	}
	return nil
}

func validateReceiptRequestBinding(receipt Receipt, request Request) error {
	expectedRequestDigest, err := RequestDigest(request)
	if err != nil {
		return fmt.Errorf("compute request digest: %w", err)
	}
	if receipt.RequestSHA256 != expectedRequestDigest || receipt.ProjectID != request.ProjectID {
		return errors.New("receipt does not match request identity")
	}
	proof := receipt.RepositoryProof
	if proof.Root != request.Root || proof.Remote != request.Remote || proof.RepositoryURL != request.RepositoryURL || proof.DefaultBranch != request.DefaultBranch || proof.Branch != request.DefaultBranch || proof.GatewayStateDir != request.GatewayStateDir {
		return errors.New("receipt repository proof does not match request")
	}
	if err := validateSessionProof(receipt.SessionProof, request.Airelay); err != nil {
		return err
	}
	if receipt.Hub.Before != request.ExpectedHubRevision {
		return errors.New("receipt hub.before does not match request expected hub revision")
	}
	return nil
}

func CanonicalHubCommittedReceiptJSON(receipt Receipt, request Request) ([]byte, error) {
	if err := ValidateHubCommittedReceipt(receipt, request); err != nil {
		return nil, err
	}
	return json.Marshal(receipt)
}

func HubCommittedReceiptDigest(receipt Receipt, request Request) (string, error) {
	data, err := CanonicalHubCommittedReceiptJSON(receipt, request)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func CanonicalActivatedReceiptJSON(receipt Receipt, request Request) ([]byte, error) {
	if err := ValidateActivatedReceipt(receipt, request); err != nil {
		return nil, err
	}
	return json.Marshal(receipt)
}

func ActivatedReceiptDigest(receipt Receipt, request Request) (string, error) {
	data, err := CanonicalActivatedReceiptJSON(receipt, request)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
