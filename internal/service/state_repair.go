package service

import "context"

// StateRepair no longer mutates the obsolete Task/Run graph. Run retirement
// is an explicit digest-guarded migration; a valid post-cutover graph has no
// automatic repair actions.
func (s *Service) StateRepair(ctx context.Context, apply bool) (StateRepairResult, error) {
	check, err := s.StateCheck(ctx)
	if err != nil {
		return StateRepairResult{}, err
	}
	return StateRepairResult{DryRun: !apply, Applied: false, OldHubSHA: check.HubRevision, Actions: []StateRepairAction{}, Check: check}, nil
}
