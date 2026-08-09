package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (r Runner) command(ctx context.Context, dir string, gitDir bool, args ...string) ([]byte, error) {
	base := []string{"-c", "core.pager=cat", "-c", "pager.log=false", "-c", "pager.show=false", "-c", "diff.external=", "-c", "color.ui=false"}
	base = append(base, args...)
	cmd := exec.CommandContext(ctx, "git", base...)
	if gitDir {
		cmd.Env = append(cleanEnv(), "GIT_DIR="+dir)
	} else {
		cmd.Dir = dir
		cmd.Env = cleanEnv()
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
func cleanEnv() []string {
	allowed := []string{"HOME", "PATH", "SSH_AUTH_SOCK", "USER", "LOGNAME", "TMPDIR"}
	out := []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C"}
	for _, k := range allowed {
		if v := os.Getenv(k); v != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}
func bounded(data []byte, max int64) (string, error) {
	if int64(len(data)) > max {
		return "", fmt.Errorf("git output exceeds %d bytes", max)
	}
	return string(data), nil
}
func validatePath(path string) error {
	if path == "" {
		return nil
	}
	return model.ValidateRelativePath(path)
}
