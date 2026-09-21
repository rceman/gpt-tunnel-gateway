package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type TaskExecutionHistoricalIntegrationInput struct {
	IntegrationHead string `json:"integration_head"`
	Profile         string `json:"profile"`
	Evidence        string `json:"evidence"`
	CandidateHead   string `json:"candidate_head,omitempty"`
	MainBase        string `json:"main_base,omitempty"`
}

type TaskExecutionIntegrateInput struct {
	ProjectID  string                                   `json:"project_id"`
	Key        string                                   `json:"key"`
	Comment    string                                   `json:"comment,omitempty"`
	Mode       string                                   `json:"mode,omitempty"`
	Historical *TaskExecutionHistoricalIntegrationInput `json:"historical,omitempty"`
}

// validateTaskExecutionIntegrateInput is the single shared boundary for both
// the durable enqueue and the synchronous worker; it runs before any state
// mutation. Historical mode proves an already-landed canonical commit from
// immutable Journal evidence and never publishes.
func validateTaskExecutionIntegrateInput(in TaskExecutionIntegrateInput) error {
	if err := validateTaskExecutionReviewInput(in.ProjectID, in.Key, "code"); err != nil {
		return err
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.Comment)) > 1024 {
		return fmt.Errorf("integration comment is too long")
	}
	switch in.Mode {
	case "", "verified":
		if in.Historical != nil {
			return fmt.Errorf("verified integration does not accept historical proof")
		}
		return nil
	case "historical":
		h := in.Historical
		if h == nil {
			return fmt.Errorf("historical integration requires proof input")
		}
		if err := model.ValidateCommitSHA(h.IntegrationHead); err != nil {
			return fmt.Errorf("historical integration_head: %w", err)
		}
		if _, _, err := model.ParseJournalID(h.Evidence); err != nil {
			return fmt.Errorf("historical evidence: %w", err)
		}
		switch h.Profile {
		case "legacy":
			if h.CandidateHead != "" || h.MainBase != "" {
				return fmt.Errorf("legacy historical integration does not accept candidate_head or main_base")
			}
		case "bootstrap_full":
			if err := model.ValidateCommitSHA(h.CandidateHead); err != nil {
				return fmt.Errorf("historical candidate_head: %w", err)
			}
			if err := model.ValidateCommitSHA(h.MainBase); err != nil {
				return fmt.Errorf("historical main_base: %w", err)
			}
		default:
			return fmt.Errorf("invalid historical integration profile")
		}
		return nil
	default:
		return fmt.Errorf("invalid integration mode")
	}
}

type taskExecutionIntegrationCapture struct {
	SchemaVersion      int                          `json:"schema_version"`
	ProjectID          string                       `json:"project_id"`
	TaskID             string                       `json:"task_id"`
	TaskRevision       int                          `json:"task_revision"`
	TaskRevisionSHA256 string                       `json:"task_revision_sha256"`
	ExecutionRevision  int                          `json:"execution_revision"`
	BaseHead           string                       `json:"base_head"`
	LaneHead           string                       `json:"lane_head"`
	Branch             string                       `json:"branch"`
	CandidateHead      string                       `json:"candidate_head"`
	CandidateTree      string                       `json:"candidate_tree"`
	GateProfileSHA256  string                       `json:"gate_profile_sha256"`
	CodeReviewID       int64                        `json:"code_review_id"`
	TestsReviewID      int64                        `json:"tests_review_id"`
	RebaseReviewID     int64                        `json:"rebase_review_id"`
	IntegrationHead    string                       `json:"integration_head,omitempty"`
	Gates              []model.CompletionGateResult `json:"gates"`
}

func (s *Service) saveTaskExecutionIntegrationCapture(ctx context.Context, capture taskExecutionIntegrationCapture) error {
	operationID := durableMutationOperationID(ctx)
	if operationID == "" {
		return nil
	}
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(capture)
	if err != nil {
		return err
	}
	operation.CapturedState = string(raw)
	operation.UpdatedAt = s.durableNow()
	return s.writeDurableMutation(operation)
}

func readTaskExecutionIntegrationCapture(operation durableMutationOperation) (taskExecutionIntegrationCapture, error) {
	if operation.CapturedState == "" {
		return taskExecutionIntegrationCapture{}, nil
	}
	var capture taskExecutionIntegrationCapture
	if err := json.Unmarshal([]byte(operation.CapturedState), &capture); err != nil || capture.SchemaVersion != 3 || capture.ProjectID == "" || capture.TaskID == "" || capture.TaskRevision < 1 || model.ValidateSHA256(capture.TaskRevisionSHA256) != nil || capture.ExecutionRevision < 1 || model.ValidateCommitSHA(capture.BaseHead) != nil || model.ValidateCommitSHA(capture.LaneHead) != nil || model.ValidateCommitSHA(capture.CandidateHead) != nil || model.ValidateCommitSHA(capture.CandidateTree) != nil || model.ValidateSHA256(capture.GateProfileSHA256) != nil || model.ValidateBranch(capture.Branch) != nil || capture.CodeReviewID < 1 || capture.TestsReviewID < 0 || capture.RebaseReviewID < 0 || len(capture.Gates) == 0 {
		return taskExecutionIntegrationCapture{}, fmt.Errorf("invalid durable Task integration capture")
	}
	if capture.IntegrationHead != "" && model.ValidateCommitSHA(capture.IntegrationHead) != nil {
		return taskExecutionIntegrationCapture{}, fmt.Errorf("invalid durable Task integration result")
	}
	seenGates := map[string]struct{}{}
	for _, gate := range capture.Gates {
		if gate.ID == "" || gate.ExitCode != 0 {
			return taskExecutionIntegrationCapture{}, fmt.Errorf("invalid durable Task gate evidence")
		}
		if _, exists := seenGates[gate.ID]; exists {
			return taskExecutionIntegrationCapture{}, fmt.Errorf("duplicate durable Task gate evidence")
		}
		seenGates[gate.ID] = struct{}{}
		if gate.TreeID != "" {
			if model.ValidateCommitSHA(gate.TreeID) != nil || gate.TreeID != capture.CandidateTree {
				return taskExecutionIntegrationCapture{}, fmt.Errorf("invalid durable Task gate tree evidence")
			}
		}
	}
	return capture, nil
}

// rejectTaskExecutionIntegration rejects an unlanded integration attempt. A
// Task that claimed integrating but never landed releases the claim back to
// verified so the canonical re-verification path (task/test reconcile,
// review, fresh task/test) remains reachable; the candidate lane is never
// touched here.
func (s *Service) rejectTaskExecutionIntegration(ctx context.Context, state model.TaskExecutionState, cause error) error {
	if state.Status != model.TaskExecutionIntegrating {
		return cause
	}
	state.Status = model.TaskExecutionVerified
	state.ExecutionRevision++
	state.UpdatedAt = s.durableNow()
	if err := s.Durability.UpdateTaskExecutionState(ctx, state, state.ExecutionRevision-1); err != nil {
		return fmt.Errorf("Task integration rejection state could not be recorded: %w (original: %v)", err, cause)
	}
	return cause
}

func taskExecutionIntegrationCaptureMatchesAdmission(capture taskExecutionIntegrationCapture, admission taskExecutionVerificationAdmission, state model.TaskExecutionState) bool {
	return capture.ProjectID == admission.identity.ProjectID && capture.TaskID == admission.identity.TaskID &&
		capture.TaskRevision == admission.identity.TaskRevision && capture.TaskRevisionSHA256 == admission.identity.TaskRevisionSHA256 &&
		capture.ExecutionRevision == state.ExecutionRevision &&
		capture.BaseHead == admission.identity.CanonicalHead && capture.LaneHead == admission.identity.Head && capture.Branch == admission.identity.Branch &&
		capture.CandidateHead == admission.identity.Head && capture.CandidateTree == admission.snapshot.tree &&
		capture.GateProfileSHA256 == admission.identity.GateProfileSHA256 &&
		capture.CodeReviewID == admission.identity.CodeReviewID && capture.TestsReviewID == admission.identity.TestsReviewID && capture.RebaseReviewID == admission.identity.RebaseReviewID
}

// taskExecutionIntegrationCaptureBindsState reports whether the durable
// capture records the exact frozen execution identity; it must hold before
// any landed-recovery or advance path may trust the capture.
func taskExecutionIntegrationCaptureBindsState(capture taskExecutionIntegrationCapture, state model.TaskExecutionState) bool {
	return capture.ProjectID == state.ProjectID && capture.TaskID == state.TaskID &&
		capture.TaskRevision == state.TaskRevision && capture.TaskRevisionSHA256 == state.TaskRevisionSHA256 &&
		capture.ExecutionRevision == state.ExecutionRevision &&
		capture.BaseHead == state.BaseHead && capture.LaneHead == state.Head && capture.Branch == state.Branch
}

// TaskExecutionIntegrate lands one verified Task candidate on canonical main.
// It never runs tests and never reconciles or rebases the lane: before any
// unlanded advance it revalidates the frozen Task, review identities, exact
// clean candidate head/tree/branch, gate profile, and canonical head against
// the admitted verification, and any mismatch rejects while leaving the
// candidate untouched. The integration commit is prepared as an object and
// durably recorded before the canonical-ref publication, which is a single
// explicit expected-old compare-and-swap permitted only because the prepared
// commit is independently proven a strict fast-forward child of the verified
// base with the exact candidate tree.
// Faults after the advance finish the same prepared integration once its
// captured identity, exact parent/tree, and reachability are proven. One
// Task lands at most one canonical commit; landing records an integration
// phase and sets integrated (pending acceptance) only.
func (s *Service) TaskExecutionIntegrate(ctx context.Context, in TaskExecutionIntegrateInput) (TaskExecutionPublicOutput, error) {
	if err := validateTaskExecutionIntegrateInput(in); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	s.taskExecutionMu.Lock()
	defer s.taskExecutionMu.Unlock()
	state, found, err := s.readExecutionForIntegration(ctx, in.ProjectID, in.Key)
	if err != nil || !found {
		if err != nil {
			return TaskExecutionPublicOutput{}, err
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task has no execution state")
	}
	project, err := s.EffectiveProjectConfig(in.ProjectID)
	if err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if in.Mode == "historical" {
		return s.taskExecutionHistoricalIntegrate(ctx, in, project, state)
	}
	if state.Status != model.TaskExecutionVerified && state.Status != model.TaskExecutionIntegrating {
		if state.Status == model.TaskExecutionIntegrated {
			return taskExecutionPublicOutput(state), nil
		}
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task is not ready for integration")
	}
	var operationCapture taskExecutionIntegrationCapture
	if operationID := durableMutationOperationID(ctx); operationID != "" {
		operation, readErr := s.readDurableMutation(operationID)
		if readErr != nil {
			return TaskExecutionPublicOutput{}, s.rejectTaskExecutionIntegration(ctx, state, readErr)
		}
		operationCapture, err = readTaskExecutionIntegrationCapture(operation)
		if err != nil {
			if state.Status == model.TaskExecutionIntegrating {
				return TaskExecutionPublicOutput{}, fmt.Errorf("durable Task integration evidence is unreadable while the recorded commit may have landed; explicit evidence reconciliation is required: %w", err)
			}
			operationCapture = taskExecutionIntegrationCapture{}
		}
	}
	branch := strings.TrimPrefix(project.DefaultBranch, "refs/heads/")
	landed := false
	landedHead := ""
	usable := operationCapture.SchemaVersion != 0 && taskExecutionIntegrationCaptureBindsState(operationCapture, state)
	if operationCapture.IntegrationHead != "" {
		canonical, refreshErr := s.Git.RefreshDefaultBranch(ctx, project)
		if refreshErr != nil {
			return TaskExecutionPublicOutput{}, refreshErr
		}
		present, presentErr := s.taskIntegrationCommitOnCanonical(ctx, project, branch, operationCapture.IntegrationHead, canonical)
		if presentErr != nil {
			return TaskExecutionPublicOutput{}, presentErr
		}
		if present && !usable {
			return TaskExecutionPublicOutput{}, fmt.Errorf("recorded Task integration commit is on canonical main but the durable evidence does not bind the current execution state; explicit evidence reconciliation is required")
		}
		if present && usable {
			exact, exactErr := s.taskIntegrationCommitExact(ctx, project, operationCapture)
			if exactErr != nil {
				return TaskExecutionPublicOutput{}, exactErr
			}
			if !exact {
				return TaskExecutionPublicOutput{}, fmt.Errorf("recorded Task integration commit on canonical main does not match the durable proof; explicit evidence reconciliation is required")
			}
			landed = true
			landedHead = canonical
		}
	}
	if !landed {
		admission, admissionErr := s.computeTaskExecutionVerificationAdmission(ctx, in.ProjectID, state)
		if admissionErr != nil {
			return TaskExecutionPublicOutput{}, s.rejectTaskExecutionIntegration(ctx, state, admissionErr)
		}
		if !usable || !taskExecutionIntegrationCaptureMatchesAdmission(operationCapture, admission, state) {
			receipt, current, _, err := s.taskExecutionVerificationProofCurrent(ctx, state)
			if err != nil {
				return TaskExecutionPublicOutput{}, s.rejectTaskExecutionIntegration(ctx, state, err)
			}
			if !current || receipt.TaskRevision != admission.identity.TaskRevision || receipt.TaskRevisionSHA256 != admission.identity.TaskRevisionSHA256 || receipt.BaseHead != admission.identity.CanonicalHead || receipt.CandidateHead != admission.identity.Head || receipt.CandidateTree != admission.snapshot.tree || receipt.GateProfileSHA256 != admission.identity.GateProfileSHA256 || receipt.CodeReviewID != admission.identity.CodeReviewID || receipt.TestsReviewID != admission.identity.TestsReviewID || receipt.RebaseReviewID != admission.identity.RebaseReviewID {
				return TaskExecutionPublicOutput{}, s.rejectTaskExecutionIntegration(ctx, state, fmt.Errorf("Task integration requires current exact verification proof"))
			}
			if _, syncErr := s.Git.SyncTaskIntegrationCheckout(ctx, project, branch, admission.identity.CanonicalHead); syncErr != nil {
				return TaskExecutionPublicOutput{}, s.rejectTaskExecutionIntegration(ctx, state, syncErr)
			}
			operationCapture = taskExecutionIntegrationCapture{
				SchemaVersion:      3,
				ProjectID:          in.ProjectID,
				TaskID:             in.Key,
				TaskRevision:       state.TaskRevision,
				TaskRevisionSHA256: state.TaskRevisionSHA256,
				ExecutionRevision:  state.ExecutionRevision,
				BaseHead:           state.BaseHead,
				LaneHead:           state.Head,
				Branch:             state.Branch,
				CandidateHead:      receipt.CandidateHead,
				CandidateTree:      receipt.CandidateTree,
				GateProfileSHA256:  receipt.GateProfileSHA256,
				CodeReviewID:       receipt.CodeReviewID,
				TestsReviewID:      receipt.TestsReviewID,
				RebaseReviewID:     receipt.RebaseReviewID,
				Gates:              receipt.Gates,
			}
			if state.Status == model.TaskExecutionVerified {
				state.Status = model.TaskExecutionIntegrating
				state.ExecutionRevision++
				state.UpdatedAt = s.durableNow()
				if s.taskIntegrationFaultHook != nil {
					if err := s.taskIntegrationFaultHook(ctx, "pretransition"); err != nil {
						return TaskExecutionPublicOutput{}, err
					}
				}
				if err := s.Durability.UpdateTaskExecutionState(ctx, state, state.ExecutionRevision-1); err != nil {
					return TaskExecutionPublicOutput{}, err
				}
				operationCapture.ExecutionRevision = state.ExecutionRevision
			}
			if err := s.taskIntegrationWriteAheadCapture(ctx, operationCapture); err != nil {
				return TaskExecutionPublicOutput{}, err
			}
		}
		if operationCapture.IntegrationHead == "" {
			if durableMutationOperationID(ctx) == "" {
				return TaskExecutionPublicOutput{}, fmt.Errorf("Task integration requires a durable operation for write-ahead evidence")
			}
			task, taskErr := s.readSharedTask(ctx, in.ProjectID, in.Key)
			if taskErr != nil {
				return TaskExecutionPublicOutput{}, taskErr
			}
			message := "Task " + task.ID + ": " + task.Title
			if comment := strings.TrimSpace(in.Comment); comment != "" {
				message += " - " + comment
			}
			prepared, err := s.Git.PrepareTaskIntegrationCommit(ctx, project, operationCapture.CandidateTree, operationCapture.BaseHead, message)
			if err != nil {
				return TaskExecutionPublicOutput{}, err
			}
			operationCapture.IntegrationHead = prepared
			if err := s.taskIntegrationWriteAheadCapture(ctx, operationCapture); err != nil {
				return TaskExecutionPublicOutput{}, fmt.Errorf("integration write-ahead evidence remains pending; retry is required: %w", err)
			}
			if s.taskIntegrationFaultHook != nil {
				if err := s.taskIntegrationFaultHook(ctx, "prepared"); err != nil {
					return TaskExecutionPublicOutput{}, err
				}
			}
		}
		exact, exactErr := s.taskIntegrationCommitExact(ctx, project, operationCapture)
		if exactErr != nil {
			return TaskExecutionPublicOutput{}, exactErr
		}
		if !exact {
			return TaskExecutionPublicOutput{}, fmt.Errorf("recorded Task integration commit does not match the durable proof; explicit evidence reconciliation is required")
		}
		canonical, refreshErr := s.Git.RefreshDefaultBranch(ctx, project)
		if refreshErr != nil {
			return TaskExecutionPublicOutput{}, refreshErr
		}
		if canonical != operationCapture.BaseHead {
			present, presentErr := s.taskIntegrationCommitOnCanonical(ctx, project, branch, operationCapture.IntegrationHead, canonical)
			if presentErr != nil {
				return TaskExecutionPublicOutput{}, presentErr
			}
			if present {
				landed = true
				landedHead = canonical
			} else {
				return TaskExecutionPublicOutput{}, s.rejectTaskExecutionIntegration(ctx, state, fmt.Errorf("canonical default branch advanced beyond the verified Task base; fresh task/test is required"))
			}
		} else {
			if s.taskIntegrationFaultHook != nil {
				if err := s.taskIntegrationFaultHook(ctx, "prepublish"); err != nil {
					return TaskExecutionPublicOutput{}, err
				}
			}
			casErr := s.Git.PushTaskIntegrationCAS(ctx, project, branch, operationCapture.BaseHead, operationCapture.CandidateTree, operationCapture.IntegrationHead)
			if s.taskIntegrationFaultHook != nil {
				if err := s.taskIntegrationFaultHook(ctx, "published"); err != nil {
					return TaskExecutionPublicOutput{}, err
				}
			}
			reread, rereadErr := s.Git.RefreshDefaultBranch(ctx, project)
			if rereadErr != nil {
				return TaskExecutionPublicOutput{}, fmt.Errorf("canonical reread after the Task publication attempt is uncertain; retry is required: %w", rereadErr)
			}
			if reread == operationCapture.IntegrationHead {
				landed = true
				landedHead = operationCapture.IntegrationHead
			} else {
				present, presentErr := s.taskIntegrationCommitOnCanonical(ctx, project, branch, operationCapture.IntegrationHead, reread)
				if presentErr != nil {
					return TaskExecutionPublicOutput{}, presentErr
				}
				switch {
				case present:
					return TaskExecutionPublicOutput{}, fmt.Errorf("exact Task integration post-publication proof failed; evidence reconciliation or retry is required")
				case casErr == nil:
					// The transport confirmed the publication, then canonical
					// moved without C: the commit definitely landed and this
					// attempt stays integrating for evidence reconciliation.
					return TaskExecutionPublicOutput{}, fmt.Errorf("exact Task integration post-publication proof failed after a confirmed publication; evidence reconciliation or retry is required")
				case !errors.Is(casErr, gitx.ErrTaskIntegrationExpectedOldMismatch):
					return TaskExecutionPublicOutput{}, fmt.Errorf("Task integration publication outcome is uncertain; retry is required: %w", casErr)
				default:
					return TaskExecutionPublicOutput{}, s.rejectTaskExecutionIntegration(ctx, state, fmt.Errorf("canonical default branch exact expected-old mismatch; fresh task/test is required: %w", casErr))
				}
			}
		}
	}
	if s.taskIntegrationFaultHook != nil {
		if err := s.taskIntegrationFaultHook(ctx, "landed"); err != nil {
			return TaskExecutionPublicOutput{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return TaskExecutionPublicOutput{}, err
	}
	if _, err := s.Git.SyncTaskIntegrationCheckout(ctx, project, branch, landedHead); err != nil {
		return TaskExecutionPublicOutput{}, fmt.Errorf("canonical worktree synchronization remains pending; retry is required: %w", err)
	}
	state.Status = model.TaskExecutionIntegrated
	state.ExecutionRevision++
	state.UpdatedAt = s.durableNow()
	phase := sqlitestore.TaskExecutionPhase{
		TaskID: state.TaskID, ProjectID: state.ProjectID, ExecutionRevision: state.ExecutionRevision,
		Stage: "integration", Status: model.TaskExecutionIntegrated, Head: operationCapture.IntegrationHead,
		Branch: state.Branch, TaskRevisionSHA256: state.TaskRevisionSHA256,
		EventKind: "integration", Decision: "accept", Comment: strings.TrimSpace(in.Comment), CreatedAt: state.UpdatedAt,
	}
	if err := s.Durability.TransitionTaskExecutionState(ctx, state, state.ExecutionRevision-1, phase); err != nil {
		return TaskExecutionPublicOutput{}, fmt.Errorf("Task integration landing state remains pending; retry is required: %w", err)
	}
	return taskExecutionPublicOutput(state), nil
}

// taskIntegrationCommitExact proves the recorded prepared commit still has its
// admitted parent and candidate tree.
func (s *Service) taskIntegrationCommitExact(ctx context.Context, project config.ProjectConfig, capture taskExecutionIntegrationCapture) (bool, error) {
	tree, parents, err := s.Git.InspectTaskIntegrationCommit(ctx, project, capture.IntegrationHead)
	if err != nil {
		return false, err
	}
	return tree == capture.CandidateTree && len(parents) == 1 && parents[0] == capture.BaseHead, nil
}

// taskIntegrationCommitOnCanonical reports whether the recorded prepared
// commit is on the canonical default branch — at its head or an ancestor of
// the current head.
func (s *Service) taskIntegrationCommitOnCanonical(ctx context.Context, project config.ProjectConfig, branch, commit, canonical string) (bool, error) {
	if commit == canonical {
		return true, nil
	}
	if err := s.Git.MaterializeMirrorCommit(ctx, project, branch, canonical); err != nil {
		return false, err
	}
	return s.Git.IsAncestor(ctx, project.Root, commit, canonical)
}

// taskIntegrationWriteAheadCapture persists the durable integration capture at
// its real semantic boundary; the seam exists so tests can fail the boundary
// deterministically without touching the shared store.
func (s *Service) taskIntegrationWriteAheadCapture(ctx context.Context, capture taskExecutionIntegrationCapture) error {
	if s.taskIntegrationWriteAhead != nil {
		return s.taskIntegrationWriteAhead(ctx, capture)
	}
	return s.saveTaskExecutionIntegrationCapture(ctx, capture)
}
