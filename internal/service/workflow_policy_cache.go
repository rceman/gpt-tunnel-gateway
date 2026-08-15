package service

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// CachedProjectWorkflowPolicy is the request-path projection of a validated
// durable policy. It is written only after a canonical Hub read or policy
// mutation succeeds; callers never supply or modify this cache.
func (s *Service) CachedProjectWorkflowPolicy(projectID string) (model.ProjectWorkflowPolicy, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	var policy model.ProjectWorkflowPolicy
	maxBytes := s.Config.MaxReadBytes
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	if err := fsutil.ReadJSONBounded(s.workflowPolicyCachePath(projectID), maxBytes, &policy); err != nil {
		return model.ProjectWorkflowPolicy{}, fmt.Errorf("local workflow policy cache for %q is unavailable: %w", projectID, err)
	}
	if err := model.ValidateProjectWorkflowPolicy(policy); err != nil {
		return model.ProjectWorkflowPolicy{}, fmt.Errorf("local workflow policy cache for %q is invalid: %w", projectID, err)
	}
	if policy.ProjectID != projectID {
		return model.ProjectWorkflowPolicy{}, fmt.Errorf("local workflow policy cache project mismatch")
	}
	return policy, nil
}

func (s *Service) cacheProjectWorkflowPolicy(policy model.ProjectWorkflowPolicy) error {
	if s.Config.StateDir == "" {
		return nil
	}
	if err := model.ValidateProjectWorkflowPolicy(policy); err != nil {
		return err
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	s.workflowPolicyCacheMu.Lock()
	defer s.workflowPolicyCacheMu.Unlock()
	return fsutil.WriteFileAtomic(s.workflowPolicyCachePath(policy.ProjectID), data, 0o600)
}

func (s *Service) workflowPolicyCachePath(projectID string) string {
	return filepath.Join(s.Config.StateDir, "cache", "workflow-policy", projectID+".json")
}
