package model

import (
	"encoding/json"
	"fmt"
	"sort"
)

func ParseRunReviewReportDraft(data []byte) (RunReviewReportDraft, error) {
	var out RunReviewReportDraft
	obj, err := strictJSONObject(data)
	if err != nil {
		return out, err
	}
	if err := validateReviewObjectKeys(obj, false); err != nil {
		return out, err
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return out, fmt.Errorf("encode review draft")
	}
	if err := json.Unmarshal(encoded, &out); err != nil {
		return out, err
	}
	if err := ValidateRunReviewReportDraft(out); err != nil {
		return out, err
	}
	return out, nil
}

func ParseRunReviewReport(data []byte) (RunReviewReport, error) {
	var out RunReviewReport
	obj, err := strictJSONObject(data)
	if err != nil {
		return out, err
	}
	if err := validateReviewObjectKeys(obj, true); err != nil {
		return out, err
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return out, fmt.Errorf("encode review report")
	}
	if err := json.Unmarshal(encoded, &out); err != nil {
		return out, err
	}
	if err := ValidateRunReviewReport(out); err != nil {
		return out, err
	}
	return out, nil
}

func validateReviewObjectKeys(obj map[string]any, final bool) error {
	allowed := map[string]bool{
		"schema_version": true, "id": true, "task_id": true, "run_id": true, "project_id": true,
		"task_sha256": true, "task_revision": true, "task_revision_sha256": true, "task_run_number": true,
		"branch": true, "base_revision": true, "reviewed_head": true,
		"repository_state": true, "gates": true, "changed_files": true, "outcome": true,
		"findings": true, "scope_coverage": true, "unexpected_surfaces": true,
		"historical_compatibility": true, "prohibited_actions": true, "next_action": true,
		"completed_sections": true, "draft_revision": true, "updated_at": true,
		"finished_at": true, "hub_commit": true,
	}
	for key := range obj {
		if !allowed[key] {
			return fmt.Errorf("unknown review report field %q", key)
		}
	}
	required := []string{"schema_version", "id", "task_id", "run_id", "project_id", "task_sha256", "branch", "base_revision", "reviewed_head", "repository_state", "gates", "changed_files"}
	if final {
		required = append(required, "outcome", "findings", "scope_coverage", "unexpected_surfaces", "historical_compatibility", "prohibited_actions", "next_action", "finished_at")
	} else {
		required = append(required, "completed_sections", "draft_revision", "updated_at")
	}
	for _, key := range required {
		if _, ok := obj[key]; !ok {
			return fmt.Errorf("missing review report field %q", key)
		}
	}
	if final {
		if _, ok := obj["completed_sections"]; ok {
			return fmt.Errorf("final review report cannot contain completed_sections")
		}
		if _, ok := obj["draft_revision"]; ok {
			return fmt.Errorf("final review report cannot contain draft_revision")
		}
		if _, ok := obj["updated_at"]; ok {
			return fmt.Errorf("final review report cannot contain updated_at")
		}
	} else {
		for _, key := range []string{"finished_at", "hub_commit"} {
			if _, ok := obj[key]; ok {
				return fmt.Errorf("draft review report cannot contain %s", key)
			}
		}
	}
	return nil
}

func CanonicalReviewSections(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}
