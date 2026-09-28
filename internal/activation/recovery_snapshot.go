package activation

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

const recoverySnapshotManifestVersion = 1

type recoverySnapshotManifest struct {
	Version                  int               `json:"version"`
	Phase                    string            `json:"phase"`
	TargetVersion            string            `json:"target_version"`
	TargetSourceSHA          string            `json:"target_source_sha"`
	CandidateChecksums       map[string]string `json:"candidate_checksums"`
	PreviousChecksums        map[string]string `json:"previous_checksums"`
	DurableSnapshotAvailable bool              `json:"durable_snapshot_available"`
	DurableStateRestored     bool              `json:"durable_state_restored"`
	CreatedAt                time.Time         `json:"created_at"`
}

type RecoverySnapshot struct {
	root         string
	candidateDir string
	previousDir  string
	stateDir     string
	manifestPath string
	manifest     recoverySnapshotManifest
}

func CreateRecoverySnapshot(pidDir, releaseDir, targetVersion, targetSourceSHA string, previous map[string][]byte) (*RecoverySnapshot, error) {
	if pidDir == "" || releaseDir == "" || targetVersion == "" || targetSourceSHA == "" || len(previous) != len(releaseartifacts.BinaryNames) {
		return nil, fmt.Errorf("activation recovery snapshot inputs are incomplete")
	}
	if err := releaseartifacts.ValidateRelease(releaseDir, targetVersion); err != nil {
		return nil, fmt.Errorf("validate candidate release for recovery snapshot: %w", err)
	}
	for _, name := range releaseartifacts.BinaryNames {
		source, modified, err := releaseartifacts.BinarySourceRevision(filepath.Join(releaseDir, name))
		if err != nil || modified || source != targetSourceSHA {
			return nil, fmt.Errorf("candidate artifact %s source provenance does not match the requested revision", name)
		}
	}
	rootBase := filepath.Join(pidDir, "activation-backups")
	if err := ensurePrivateRecoveryDirectory(rootBase); err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp(rootBase, "activation-")
	if err != nil {
		return nil, err
	}
	snapshot := &RecoverySnapshot{
		root:         root,
		candidateDir: filepath.Join(root, "candidate"),
		previousDir:  filepath.Join(root, "previous"),
		stateDir:     filepath.Join(root, "state"),
		manifestPath: filepath.Join(root, "activation.json"),
		manifest: recoverySnapshotManifest{
			Version:            recoverySnapshotManifestVersion,
			Phase:              "prepared",
			TargetVersion:      targetVersion,
			TargetSourceSHA:    targetSourceSHA,
			CandidateChecksums: map[string]string{},
			PreviousChecksums:  map[string]string{},
			CreatedAt:          time.Now().UTC(),
		},
	}
	if err := snapshot.prepare(releaseDir, previous); err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	return snapshot, nil
}

func (s *RecoverySnapshot) ID() string {
	if s == nil {
		return ""
	}
	return filepath.Base(s.root)
}

func (s *RecoverySnapshot) CaptureDurableState(ctx context.Context, stateDir string) error {
	if s == nil {
		return fmt.Errorf("activation recovery snapshot is unavailable")
	}
	if err := sqlitestore.SnapshotDatabases(ctx, stateDir, s.stateDir); err != nil {
		return err
	}
	s.manifest.DurableSnapshotAvailable = true
	s.manifest.Phase = "state_snapshotted"
	return s.writeManifest()
}

func (s *RecoverySnapshot) RestoreDurableState(stateDir string) error {
	if s == nil || !s.manifest.DurableSnapshotAvailable {
		return fmt.Errorf("pre-mutation durable snapshot is unavailable")
	}
	s.manifest.Phase = "restoring_state"
	if err := s.writeManifest(); err != nil {
		return err
	}
	if err := sqlitestore.RestoreDatabases(s.stateDir, stateDir); err != nil {
		return err
	}
	s.manifest.DurableStateRestored = true
	s.manifest.Phase = "state_restored"
	return s.writeManifest()
}

func (s *RecoverySnapshot) ReplaceCandidate(paths map[string]string) error {
	if s == nil {
		return fmt.Errorf("activation recovery snapshot is unavailable")
	}
	if err := s.verifyCandidateSnapshot(); err != nil {
		return err
	}
	s.manifest.Phase = "candidate_installing"
	if err := s.writeManifest(); err != nil {
		return err
	}
	previous, err := s.PreviousArtifacts()
	if err != nil {
		return err
	}
	if err := releaseartifacts.ReplaceAll(s.candidateDir, paths, previous); err != nil {
		return err
	}
	s.manifest.Phase = "candidate_installed"
	return s.writeManifest()
}

func (s *RecoverySnapshot) RestorePrevious(paths map[string]string) error {
	if s == nil {
		return fmt.Errorf("activation recovery snapshot is unavailable")
	}
	s.manifest.Phase = "restoring_previous_artifacts"
	if err := s.writeManifest(); err != nil {
		return err
	}
	previous, err := s.PreviousArtifacts()
	if err != nil {
		return err
	}
	if err := releaseartifacts.RestoreAll(paths, previous); err != nil {
		return err
	}
	s.manifest.Phase = "previous_artifacts_restored"
	return s.writeManifest()
}

func (s *RecoverySnapshot) verifyCandidateSnapshot() error {
	if s == nil {
		return fmt.Errorf("activation recovery snapshot is unavailable")
	}
	if err := releaseartifacts.ValidateRelease(s.candidateDir, s.manifest.TargetVersion); err != nil {
		return fmt.Errorf("candidate recovery artifact set failed verification")
	}
	for _, name := range releaseartifacts.BinaryNames {
		checksum, err := releaseartifacts.HashFile(filepath.Join(s.candidateDir, name))
		if err != nil || checksum != s.manifest.CandidateChecksums[name] {
			return fmt.Errorf("candidate recovery artifact %s failed verification", name)
		}
		source, modified, err := releaseartifacts.BinarySourceRevision(filepath.Join(s.candidateDir, name))
		if err != nil || modified || source != s.manifest.TargetSourceSHA {
			return fmt.Errorf("candidate recovery artifact %s source proof failed", name)
		}
	}
	return nil
}

func (s *RecoverySnapshot) VerifyCandidate(paths map[string]string) error {
	if err := s.verifyCandidateSnapshot(); err != nil {
		return err
	}
	if err := releaseartifacts.VerifyInstalled(s.candidateDir, paths); err != nil {
		return err
	}
	for _, name := range releaseartifacts.BinaryNames {
		source, modified, err := releaseartifacts.BinarySourceRevision(paths[name])
		if err != nil || modified || source != s.manifest.TargetSourceSHA {
			return fmt.Errorf("installed candidate %s source proof failed", name)
		}
	}
	s.manifest.Phase = "candidate_verified"
	return s.writeManifest()
}

func (s *RecoverySnapshot) VerifyPrevious(paths map[string]string) error {
	if s == nil {
		return fmt.Errorf("activation recovery snapshot is unavailable")
	}
	if _, err := s.PreviousArtifacts(); err != nil {
		return err
	}
	if err := releaseartifacts.VerifyInstalled(s.previousDir, paths); err != nil {
		return err
	}
	s.manifest.Phase = "previous_verified"
	return s.writeManifest()
}

func (s *RecoverySnapshot) PreviousArtifacts() (map[string][]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("activation recovery snapshot is unavailable")
	}
	previous := make(map[string][]byte, len(releaseartifacts.BinaryNames))
	for _, name := range releaseartifacts.BinaryNames {
		path := filepath.Join(s.previousDir, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("previous artifact snapshot %s is unavailable or unsafe", name)
		}
		checksum, err := releaseartifacts.HashFile(path)
		if err != nil || checksum != s.manifest.PreviousChecksums[name] {
			return nil, fmt.Errorf("previous artifact snapshot %s failed verification", name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read previous artifact snapshot %s: %w", name, err)
		}
		previous[name] = data
	}
	return previous, nil
}

func (s *RecoverySnapshot) Cleanup() error {
	if s == nil || s.root == "" {
		return nil
	}
	parent := filepath.Dir(s.root)
	if err := os.RemoveAll(s.root); err != nil {
		return err
	}
	if directory, err := os.Open(parent); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func (s *RecoverySnapshot) prepare(releaseDir string, previous map[string][]byte) error {
	if err := os.Mkdir(s.candidateDir, 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(s.previousDir, 0o700); err != nil {
		return err
	}
	if err := syncRecoveryDirectory(s.root); err != nil {
		return err
	}
	for _, name := range append(append([]string(nil), releaseartifacts.BinaryNames...), "SHA256SUMS") {
		mode := os.FileMode(0o700)
		if name == "SHA256SUMS" {
			mode = 0o600
		}
		if err := copyRecoveryArtifact(filepath.Join(releaseDir, name), filepath.Join(s.candidateDir, name), mode); err != nil {
			return err
		}
		checksum, err := releaseartifacts.HashFile(filepath.Join(s.candidateDir, name))
		if err != nil {
			return err
		}
		if name != "SHA256SUMS" {
			s.manifest.CandidateChecksums[name] = checksum
		}
	}
	if err := releaseartifacts.ValidateRelease(s.candidateDir, s.manifest.TargetVersion); err != nil {
		return fmt.Errorf("candidate recovery copy is invalid: %w", err)
	}
	for _, name := range releaseartifacts.BinaryNames {
		data, ok := previous[name]
		if !ok || len(data) == 0 {
			return fmt.Errorf("previous artifact snapshot %s is unavailable", name)
		}
		path := filepath.Join(s.previousDir, name)
		if err := fsutil.WriteFileAtomic(path, data, 0o700); err != nil {
			return err
		}
		checksum, err := releaseartifacts.HashFile(path)
		if err != nil {
			return err
		}
		s.manifest.PreviousChecksums[name] = checksum
	}
	if err := s.writeManifest(); err != nil {
		return err
	}
	return nil
}

func (s *RecoverySnapshot) writeManifest() error {
	return fsutil.WriteJSONAtomic(s.manifestPath, s.manifest, 0o600)
}

func copyRecoveryArtifact(sourcePath, destinationPath string, mode os.FileMode) error {
	info, err := os.Lstat(sourcePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("release artifact is unavailable or unsafe")
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	temporary, err := os.CreateTemp(filepath.Dir(destinationPath), ".artifact-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := func(primary error) error {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		return primary
	}
	if err := temporary.Chmod(mode); err != nil {
		return cleanup(err)
	}
	if _, err := io.Copy(temporary, source); err != nil {
		return cleanup(err)
	}
	if err := temporary.Sync(); err != nil {
		return cleanup(err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := os.Rename(temporaryPath, destinationPath); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return syncRecoveryDirectory(filepath.Dir(destinationPath))
}

func ensurePrivateRecoveryDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("activation recovery directory is unavailable or unsafe")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	info, err = os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("activation recovery directory must be owner-only")
	}
	return syncRecoveryDirectory(filepath.Dir(path))
}

func syncRecoveryDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
