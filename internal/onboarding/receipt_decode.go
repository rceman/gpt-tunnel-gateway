package onboarding

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

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
