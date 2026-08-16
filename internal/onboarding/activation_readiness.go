package onboarding

import (
	"context"
	"errors"
	"strconv"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func managedEntryForRequest(request Request) config.ManagedProjectEntry {
	return config.ManagedProjectEntry{Root: request.Root, RepositoryURL: request.RepositoryURL, Remote: request.Remote, DefaultBranch: request.DefaultBranch, ProjectCode: request.ProjectCode, AirelaySessionKey: sessionKey(request)}
}

func sessionKey(request Request) string {
	if request.Airelay.SessionKey == nil {
		return ""
	}
	return *request.Airelay.SessionKey
}

func mirrorProofFromReceipt(receipt Receipt) MirrorProof {
	if receipt.MirrorProof == nil {
		return MirrorProof{}
	}
	return *receipt.MirrorProof
}

func sameSessionProof(left, right SessionProof) bool {
	if left.Required != right.Required || left.Status != right.Status || left.SessionKey == nil != (right.SessionKey == nil) || left.ControllerProtocolVersion == nil != (right.ControllerProtocolVersion == nil) {
		return false
	}
	if left.SessionKey != nil && *left.SessionKey != *right.SessionKey {
		return false
	}
	if left.ControllerProtocolVersion != nil && *left.ControllerProtocolVersion != *right.ControllerProtocolVersion {
		return false
	}
	return true
}

func (c *ActivationCoordinator) defaultProjectReadiness(ctx context.Context, request Request, local config.ProjectConfig, project model.Project, plan model.Plan, identifiers model.ProjectIdentifiers) error {
	if err := model.ValidateProject(project); err != nil {
		return err
	}
	if err := model.ValidatePlan(plan); err != nil {
		return err
	}
	if err := model.ValidateProjectIdentifiers(identifiers); err != nil {
		return err
	}
	if local.Root != request.Root || local.Remote != request.Remote || local.DefaultBranch != request.DefaultBranch || local.AirelaySessionKey != sessionKey(request) {
		return errors.New("effective project metadata does not match request")
	}
	status, err := c.Git.WorktreeStatus(ctx, local)
	if err != nil {
		return err
	}
	if !status.Clean {
		return errors.New("project worktree is dirty")
	}
	remoteURL, err := c.Git.RemoteURL(ctx, local)
	if err != nil {
		return err
	}
	if remoteURL != request.RepositoryURL {
		return errors.New("project remote URL does not match request")
	}
	branch, err := c.Git.RemoteDefaultBranch(ctx, local)
	if err != nil {
		return err
	}
	if branch != request.DefaultBranch {
		return errors.New("project remote default branch does not match request")
	}
	return nil
}

func (c *ActivationCoordinator) defaultSessionReadiness(ctx context.Context, request Request) (SessionProof, error) {
	if !request.Airelay.SessionRequired {
		return SessionProof{
			Required: false,
			Status:   "not_required",
		}, nil
	}
	key := sessionKey(request)
	status, err := c.Airelay.Status(ctx, key)
	if err != nil {
		return SessionProof{}, err
	}
	switch status.State {
	case "running", "waiting", "idle":
	default:
		return SessionProof{}, errors.New("Airelay session is not explicitly ready")
	}
	if !status.ControllerReachable || status.ExitCode != 0 {
		return SessionProof{}, errors.New("Airelay session is not explicitly ready")
	}
	protocol := PositiveInteger(1)
	parsed, err := strconv.ParseUint(status.ProtocolVersion, 10, 64)
	if err != nil || parsed < 1 || parsed > MaxSafeInteger {
		return SessionProof{}, errors.New("Airelay status has no valid positive protocol version")
	}
	protocol = PositiveInteger(parsed)
	return SessionProof{
		Required:                  true,
		SessionKey:                &key,
		Status:                    "active",
		ControllerProtocolVersion: &protocol,
	}, nil
}
