package service

// LifecycleConflictError is the bounded machine-readable form of an
// optimistic lifecycle guard failure. Details contain only fields needed to
// decide whether the caller has a stale view or hit a transaction race.
type LifecycleConflictError struct {
	Code    string         `json:"code"`
	Phase   string         `json:"phase,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *LifecycleConflictError) Error() string { return e.Code }

func (e *LifecycleConflictError) StructuredActionError() map[string]any {
	return map[string]any{"code": e.Code, "phase": e.Phase, "details": e.Details}
}

func trainRevisionStatusConflict(phase, guard string, expected, current int, status string) error {
	return &LifecycleConflictError{
		Code:  "TRAIN_REVISION_STATUS_CONFLICT",
		Phase: phase,
		Details: map[string]any{
			"guard": guard, "expected_revision": expected,
			"current_revision": current, "current_status": status,
		},
	}
}

func trainIntegrationReceiptConflict(phase string, revision int, status, receiptStatus string) error {
	return &LifecycleConflictError{
		Code:  "TRAIN_INTEGRATION_RECEIPT_CONFLICT",
		Phase: phase,
		Details: map[string]any{
			"current_revision": revision, "current_status": status,
			"receipt_status": receiptStatus,
		},
	}
}
