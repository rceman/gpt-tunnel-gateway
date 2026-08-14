package controller

import (
	"io"
	"os"
	"path/filepath"
)

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(dst), ".gateway-backup-*")
	if err != nil {
		return err
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Chmod(0o755); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
func (c Controller) RestartGateway() error {
	_, err := c.RestartGatewayRecovery("")
	return err
}

// RestartGatewayAfterUpgrade stops the exact gateway recorded by the controller
// and starts the currently installed binary. It intentionally does not touch the
// tunnel process; callers own rollback of the installed gateway binary.
func (c Controller) RestartGatewayAfterUpgrade() error {
	_, err := c.RestartGatewayAfterUpgradeDiagnostics()
	return err
}
