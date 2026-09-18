package service

import (
	"context"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// Correction creation is retired with the Task/Run execution graph. Correction
// admission must target a canonical Task execution.
func (s *Service) TaskCorrectionCreate(context.Context, TaskCorrectionCreateInput) (model.TaskRevision, OperationResult, error) {
	return model.TaskRevision{}, OperationResult{}, errRunAuthorityRetired
}

func nowUTC() time.Time { return time.Now().UTC() }
