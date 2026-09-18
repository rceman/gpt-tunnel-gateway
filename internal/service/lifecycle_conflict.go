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
