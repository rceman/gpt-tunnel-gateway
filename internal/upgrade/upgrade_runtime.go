package upgrade

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func serviceHubRevision(ctx context.Context, c config.Config) (string, error) {
	return service.New(c).Hub.RemoteRevision(ctx)
}
func parseVersion(v string) (string, error) {
	if !semverRE.MatchString(v) {
		return "", fmt.Errorf("invalid target version")
	}
	return v, nil
}
func compareVersion(a, b string) int {
	var x, y [3]int
	fmt.Sscanf(a, "%d.%d.%d", &x[0], &x[1], &x[2])
	fmt.Sscanf(b, "%d.%d.%d", &y[0], &y[1], &y[2])
	for i := 0; i < 3; i++ {
		if x[i] < y[i] {
			return -1
		}
		if x[i] > y[i] {
			return 1
		}
	}
	return 0
}
func installedVersion(path string) (string, error) {
	v, err := releaseartifacts.BinaryVersion(path)
	if err != nil {
		return "", err
	}
	if !semverRE.MatchString(v) {
		return "", fmt.Errorf("invalid installed version")
	}
	return v, nil
}

func validateInstalledRuntime(c config.Config) (string, error) {
	home, _ := os.UserHomeDir()
	canonicalDir := filepath.Join(home, ".local", "bin")
	paths := []string{filepath.Join(canonicalDir, "gpt-tunnel-gatewayd"), filepath.Join(canonicalDir, "gpt-tunnel"), filepath.Join(canonicalDir, "gpt-tunnelctl")}
	if filepath.Clean(c.Controller.GatewayBinary) != paths[0] {
		return "", fmt.Errorf("gateway binary is not at canonical install path")
	}
	if err := validateOwnedExecutable(c.Controller.TunnelClientBinary, "tunnel-client"); err != nil {
		return "", err
	}
	versions := make([]string, len(paths))
	for i, path := range paths {
		if err := validateOwnedExecutable(path, "installed binary"); err != nil {
			return "", err
		}
		var err error
		versions[i], err = installedVersion(path)
		if err != nil {
			return "", err
		}
	}
	if versions[0] != versions[1] || versions[0] != versions[2] {
		return "", fmt.Errorf("installed binary versions disagree")
	}
	return versions[2], nil
}

func validateOwnedExecutable(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s is not available", label)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a regular executable", label)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("%s owner mismatch", label)
	}
	return nil
}

func validatePIDFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("invalid PID file")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("PID file owner mismatch")
	}
	return nil
}
func verifyInstalledProof(paths map[string]string, version string, expectedHashes map[string]string, protectedPaths []string, protectedHashes map[string]string, tunnelPID int, status controller.Status) error {
	for _, name := range binaryOrder {
		got, err := fileHash(paths[name])
		if err != nil {
			return err
		}
		if got != expectedHashes[name] {
			return fmt.Errorf("binary hash proof failed")
		}
		v, err := installedVersion(paths[name])
		if err != nil || v != version {
			return fmt.Errorf("binary version proof failed")
		}
	}
	if status.Tunnel.PID != tunnelPID || !status.Gateway.Running || !status.Gateway.IdentityValid || status.Gateway.Executable != paths["gpt-tunnel-gatewayd"] || status.RunningVersion != version || !status.VersionMatch {
		return fmt.Errorf("runtime identity proof failed")
	}
	for _, path := range protectedPaths {
		got, err := fileHash(path)
		if err != nil || got != protectedHashes[path] {
			return fmt.Errorf("protected runtime hash changed")
		}
	}
	return nil
}
