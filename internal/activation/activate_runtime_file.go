package activation

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/mcpmanifest"
	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
)

const (
	OutputLimit                     = 1 << 20
	activationSubprocessOutputLimit = 16 << 10
)

var canonicalRuntimeTools = mcpmanifest.CanonicalToolNames()

type Result struct {
	SourceHead string `json:"source_head"`
	Activation string `json:"activation"`
	Smoke      string `json:"smoke"`
	TunnelPID  int    `json:"tunnel_pid,omitempty"`
	GatewayPID int    `json:"gateway_pid,omitempty"`
}

// ProveSource verifies that the requested source is already the live runtime
// without changing installed artifacts or restarting a process. It is used by
// in-process control-plane mutations, where calling Source would terminate
// the serving gateway before the mutation can commit.
func ProveSource(ctx context.Context, c config.Config, configPath string, project config.ProjectConfig, sourceHead string) (Result, error) {
	if project.Root == "" || sourceHead == "" {
		return Result{}, fmt.Errorf("activation source is incomplete")
	}
	if got, err := gitOutput(ctx, project.Root, "rev-parse", "--verify", "HEAD^{commit}"); err != nil || got != sourceHead {
		return Result{}, fmt.Errorf("project source head is not the reviewed head")
	}
	if dirty, err := gitOutput(ctx, project.Root, "status", "--porcelain", "--untracked-files=all"); err != nil || dirty != "" {
		return Result{}, fmt.Errorf("project source worktree is dirty")
	}
	versionBytes, err := os.ReadFile(filepath.Join(project.Root, "VERSION"))
	if err != nil {
		return Result{}, err
	}
	targetVersion := strings.TrimSpace(string(versionBytes))
	if targetVersion == "" {
		return Result{}, fmt.Errorf("project VERSION is empty")
	}
	release, err := os.MkdirTemp("", "gpt-tunnel-source-proof-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(release)
	build := exec.CommandContext(ctx, filepath.Join(project.Root, "scripts", "build-release.sh"), release)
	build.Dir = project.Root
	if output, err := runBoundedCommand(build); err != nil {
		return Result{}, fmt.Errorf("source proof build failed: %s", BoundedOutput(output))
	}
	builtGateway := filepath.Join(release, "gpt-tunnel-gatewayd")
	installedGateway := c.Controller.GatewayBinary
	builtSHA, err := sha256File(builtGateway)
	if err != nil {
		return Result{}, fmt.Errorf("source proof artifact checksum failed: %w", err)
	}
	installedSHA, err := sha256File(installedGateway)
	if err != nil {
		return Result{}, fmt.Errorf("installed gateway checksum failed: %w", err)
	}
	if builtSHA != installedSHA {
		return Result{}, fmt.Errorf("installed gateway does not match exact source proof: built_sha256=%s installed_sha256=%s", builtSHA, installedSHA)
	}
	ctl := controller.Controller{Config: c, ConfigPath: configPath}
	status, err := ctl.Status(ctx)
	if err != nil {
		return Result{}, err
	}
	if !status.Gateway.Running || !status.Tunnel.Running || !status.GatewayReady || !status.TunnelReady || !status.VersionMatch || status.InstalledVersion != targetVersion || status.RunningVersion != targetVersion {
		return Result{}, fmt.Errorf("runtime is not healthy and version-matched for the reviewed source")
	}
	if err := ctl.Doctor(ctx); err != nil {
		return Result{}, err
	}
	if err := LiveMCPSmoke(ctx, c, targetVersion); err != nil {
		return Result{}, err
	}
	return Result{
		SourceHead: sourceHead,
		Activation: "already_active",
		Smoke:      "passed",
		TunnelPID:  status.Tunnel.PID,
		GatewayPID: status.Gateway.PID,
	}, nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// SelfActivate builds and activates one exact GTW source revision into an
// external operation directory. It performs offline verification first, then
// holds the controller handoff lock across Gateway stop, atomic replacement,
// start, readiness/provenance proof, and rollback. Tunnel is never touched.
func SelfActivate(ctx context.Context, c config.Config, configPath string, project config.ProjectConfig, sourceHead string) (Result, error) {
	return selfActivate(ctx, c, configPath, project, sourceHead, false, true)
}

// DebugActivate is the explicit host-local break-glass activation path. It
// keeps the canonical artifact/smoke/atomic Gateway handoff but requires the
// configured Gateway source to be exactly on main. A broken Gateway may be
// repaired; the existing Tunnel must remain running and ready.
func DebugActivate(ctx context.Context, c config.Config, configPath string, project config.ProjectConfig, sourceHead string) (Result, error) {
	return selfActivate(ctx, c, configPath, project, sourceHead, true, false)
}

func selfActivate(ctx context.Context, c config.Config, configPath string, project config.ProjectConfig, sourceHead string, requireMainBranch, requireGatewayHealthy bool) (Result, error) {
	if project.Root == "" || sourceHead == "" {
		return Result{}, fmt.Errorf("activation source is incomplete")
	}
	if requireMainBranch {
		branch, err := gitOutput(ctx, project.Root, "symbolic-ref", "--quiet", "--short", "HEAD")
		if err != nil || branch != "main" {
			return Result{}, fmt.Errorf("debug activation requires the configured source branch to be main")
		}
	}
	if got, err := gitOutput(ctx, project.Root, "rev-parse", "--verify", "HEAD^{commit}"); err != nil || got != sourceHead {
		return Result{}, fmt.Errorf("project source head is not the reviewed head")
	}
	if dirty, err := gitOutput(ctx, project.Root, "status", "--porcelain", "--untracked-files=all"); err != nil || dirty != "" {
		return Result{}, fmt.Errorf("project source worktree is dirty")
	}
	versionBytes, err := os.ReadFile(filepath.Join(project.Root, "VERSION"))
	if err != nil {
		return Result{}, err
	}
	targetVersion := strings.TrimSpace(string(versionBytes))
	if targetVersion == "" {
		return Result{}, fmt.Errorf("project VERSION is empty")
	}
	ctl := controller.Controller{Config: c, ConfigPath: configPath}
	before, err := ctl.Status(ctx)
	if err != nil {
		return Result{}, err
	}
	if !before.Tunnel.Running || !before.TunnelReady {
		return Result{}, fmt.Errorf("tunnel is not healthy before activation")
	}
	if requireGatewayHealthy && (!before.Gateway.Running || !before.GatewayReady) {
		return Result{}, fmt.Errorf("runtime is not healthy before activation")
	}
	release, err := os.MkdirTemp("", "gpt-tunnel-activation-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(release)
	build := exec.CommandContext(ctx, filepath.Join(project.Root, "scripts", "build-release.sh"), release)
	build.Dir = project.Root
	output, err := runBoundedCommand(build)
	if err != nil {
		return Result{}, fmt.Errorf("release build failed: %s", BoundedOutput(output))
	}
	if err := releaseartifacts.ValidateRelease(release, targetVersion); err != nil {
		return Result{}, err
	}
	if requireMainBranch {
		if err := validateReleaseSource(release, sourceHead); err != nil {
			return Result{}, err
		}
	}
	if err := SmokeCandidate(ctx, c, filepath.Join(release, "gpt-tunnel-gatewayd"), targetVersion); err != nil {
		return Result{}, err
	}
	paths := releaseartifacts.Paths(c.Controller.GatewayBinary)
	old, err := releaseartifacts.SnapshotAll(paths)
	if err != nil {
		return Result{}, err
	}
	recovery, err := CreateRecoverySnapshot(c.Controller.PIDDir, release, targetVersion, sourceHead, old)
	if err != nil {
		return Result{}, err
	}
	var after controller.Status
	outcome, activationErr := ctl.ActivateGateway(controller.GatewayActivation{
		ValidateBeforeStop: func() error {
			current, err := ctl.Status(ctx)
			if err != nil {
				return err
			}
			if !current.Tunnel.Running || !current.TunnelReady || current.Tunnel.PID != before.Tunnel.PID {
				return fmt.Errorf("Tunnel identity or readiness changed after candidate preflight")
			}
			if requireGatewayHealthy && (!current.Gateway.Running || !current.GatewayReady || !current.VersionMatch || !current.RuntimeIdentity.ArtifactSetCoherent || !current.RuntimeIdentity.RunningGatewayMatchesInstall || current.RuntimeIdentity.RunningExecutableSHA256 != current.RuntimeIdentity.InstalledGatewaySHA256 || current.Gateway.PID != before.Gateway.PID) {
				return fmt.Errorf("serving Gateway changed after candidate preflight")
			}
			return recovery.VerifyPrevious(paths)
		},
		SnapshotState: func() error {
			return recovery.CaptureDurableState(ctx, c.StateDir)
		},
		RestoreState: func() error {
			return recovery.RestoreDurableState(c.StateDir)
		},
		Replace: func() error {
			return recovery.ReplaceCandidate(paths)
		},
		Restore: func() error {
			return recovery.RestorePrevious(paths)
		},
		Verify: func() error {
			if err := recovery.VerifyCandidate(paths); err != nil {
				return err
			}
			var statusErr error
			after, statusErr = ctl.Status(ctx)
			if statusErr != nil {
				return statusErr
			}
			if after.Tunnel.PID != before.Tunnel.PID || !after.Gateway.Running || !after.GatewayReady || !after.TunnelReady || !after.VersionMatch || after.InstalledVersion != targetVersion || after.RunningVersion != targetVersion || !after.RuntimeIdentity.ExactSourceMatch || after.RuntimeIdentity.SourceSHA != sourceHead || !after.RuntimeIdentity.ArtifactSetCoherent || !after.RuntimeIdentity.RunningGatewayMatchesInstall || after.RuntimeIdentity.RunningExecutableSHA256 != after.RuntimeIdentity.InstalledGatewaySHA256 {
				return fmt.Errorf("activation runtime identity/readiness/source proof failed")
			}
			if err := ctl.Doctor(ctx); err != nil {
				return err
			}
			return LiveMCPSmoke(ctx, c, targetVersion)
		},
		VerifyRollback: func() error {
			if !before.Gateway.Running || !before.GatewayReady || !before.VersionMatch {
				return fmt.Errorf("previous Gateway was not healthy; compatible-forward recovery is required")
			}
			if err := recovery.VerifyPrevious(paths); err != nil {
				return err
			}
			previous, err := ctl.Status(ctx)
			if err != nil {
				return err
			}
			if previous.Tunnel.PID != before.Tunnel.PID || !previous.Gateway.Running || !previous.GatewayReady || !previous.TunnelReady || !previous.VersionMatch || previous.InstalledVersion != before.InstalledVersion || previous.RunningVersion != before.RunningVersion || !previous.RuntimeIdentity.RunningGatewayMatchesInstall || previous.RuntimeIdentity.RunningExecutableSHA256 != previous.RuntimeIdentity.InstalledGatewaySHA256 {
				return fmt.Errorf("previous Gateway artifact and Tunnel identity proof failed")
			}
			if before.RuntimeIdentity.ExactSourceMatch && (!previous.RuntimeIdentity.ExactSourceMatch || previous.RuntimeIdentity.SourceSHA != before.RuntimeIdentity.SourceSHA) {
				return fmt.Errorf("previous Gateway source identity proof failed")
			}
			if err := ctl.Doctor(ctx); err != nil {
				return err
			}
			return LiveMCPSmoke(ctx, c, before.InstalledVersion)
		},
	})
	if activationErr != nil {
		if outcome.ForwardRecovery == controller.GatewayForwardRecoveryRequired || outcome.ForwardRecovery == controller.GatewayForwardRecoveryServingCandidate {
			return Result{}, fmt.Errorf("%w; activation recovery snapshot retained id=%s", activationErr, recovery.ID())
		}
		if cleanupErr := recovery.Cleanup(); cleanupErr != nil {
			return Result{}, fmt.Errorf("%w; activation recovery snapshot %s cleanup failed: %v", activationErr, recovery.ID(), cleanupErr)
		}
		return Result{}, activationErr
	}
	if err := recovery.Cleanup(); err != nil {
		return Result{}, fmt.Errorf("activation succeeded; recovery snapshot %s retained: %w", recovery.ID(), err)
	}
	return Result{
		SourceHead: sourceHead,
		Activation: "passed",
		Smoke:      "passed",
		TunnelPID:  after.Tunnel.PID,
		GatewayPID: after.Gateway.PID,
	}, nil
}

func validateReleaseSource(dir, sourceHead string) error {
	for _, name := range releaseartifacts.BinaryNames {
		got, modified, err := releaseartifacts.BinarySourceRevision(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("release artifact %s has no exact source provenance: %w", name, err)
		}
		if modified || got != sourceHead {
			return fmt.Errorf("release artifact %s source=%q want %q", name, got, sourceHead)
		}
	}
	return nil
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	out, err := runBoundedCommand(cmd)
	return strings.TrimSpace(string(out)), err
}

func binaryVersion(path string) (string, error) {
	return releaseartifacts.BinaryVersion(path)
}

func runBoundedCommand(command *exec.Cmd) ([]byte, error) {
	var output boundedBuffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if output.exceeded {
		return output.Bytes(), fmt.Errorf("subprocess output exceeds %d bytes", activationSubprocessOutputLimit)
	}
	return output.Bytes(), err
}

func BoundedOutput(data []byte) string {
	if len(data) > OutputLimit {
		data = data[:OutputLimit]
	}
	return strings.TrimSpace(string(data))
}

// LiveMCPSmoke proves the canonical public MCP runtime contract used by both
// activation and transactional upgrade/rollback verification.
