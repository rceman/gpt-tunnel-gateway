package gitx

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

var ErrStreamLimit = errors.New("git stream page limit reached")

func (r Runner) command(ctx context.Context, dir string, gitDir bool, args ...string) ([]byte, error) {
	return r.commandWithEnv(ctx, dir, gitDir, nil, args...)
}

func (r Runner) commandWithEnv(ctx context.Context, dir string, gitDir bool, extraEnv []string, args ...string) ([]byte, error) {
	base := []string{"-c", "core.pager=cat", "-c", "pager.log=false", "-c", "pager.show=false", "-c", "diff.external=", "-c", "color.ui=false"}
	base = append(base, args...)
	cmd := exec.CommandContext(ctx, "git", base...)
	if gitDir {
		cmd.Env = append(cleanEnv(), "GIT_DIR="+dir)
	} else {
		cmd.Dir = dir
		cmd.Env = cleanEnv()
	}
	cmd.Env = append(cmd.Env, extraEnv...)
	stdout := boundedCommandBuffer{limit: r.MaxReadBytes}
	stderr := boundedCommandBuffer{limit: r.MaxReadBytes}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stdout.exceeded || stderr.exceeded {
			return nil, fmt.Errorf("git output exceeds %d bytes", r.MaxReadBytes)
		}
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	if stdout.exceeded || stderr.exceeded {
		return nil, fmt.Errorf("git output exceeds %d bytes", r.MaxReadBytes)
	}
	return stdout.data, nil
}

func (r Runner) commandRecords(ctx context.Context, dir string, gitDir bool, delimiter byte, args ...string) (func(func(string) error) error, error) {
	return func(visit func(string) error) error {
		base := []string{"-c", "core.pager=cat", "-c", "pager.log=false", "-c", "pager.show=false", "-c", "diff.external=", "-c", "color.ui=false"}
		base = append(base, args...)
		cmd := exec.CommandContext(ctx, "git", base...)
		if gitDir {
			cmd.Env = append(cleanEnv(), "GIT_DIR="+dir)
		} else {
			cmd.Dir = dir
			cmd.Env = cleanEnv()
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		stderr := boundedCommandBuffer{limit: r.MaxReadBytes}
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		reader := bufio.NewReader(stdout)
		for {
			record, readErr := reader.ReadString(delimiter)
			if len(record) > 0 {
				record = strings.TrimSuffix(record, string(delimiter))
				if err := visit(record); err != nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					return err
				}
			}
			if readErr != nil {
				if readErr == io.EOF {
					break
				}
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return readErr
			}
		}
		if err := cmd.Wait(); err != nil {
			if stderr.exceeded {
				return fmt.Errorf("git output exceeds %d bytes", r.MaxReadBytes)
			}
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return nil
	}, nil
}

func (r Runner) streamCommand(ctx context.Context, dir string, gitDir bool, args []string, visit func([]byte) error) (int, error) {
	base := []string{"-c", "core.pager=cat", "-c", "pager.log=false", "-c", "pager.show=false", "-c", "diff.external=", "-c", "color.ui=false"}
	base = append(base, args...)
	cmd := exec.CommandContext(ctx, "git", base...)
	if gitDir {
		cmd.Env = append(cleanEnv(), "GIT_DIR="+dir)
	} else {
		cmd.Dir = dir
		cmd.Env = cleanEnv()
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr := boundedCommandBuffer{limit: r.MaxReadBytes}
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	buf := make([]byte, 32<<10)
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 {
			if err := visit(buf[:n]); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				if errors.Is(err, ErrStreamLimit) {
					return 0, nil
				}
				return -1, err
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return -1, readErr
			}
			break
		}
	}
	if err := cmd.Wait(); err != nil {
		if stderr.exceeded {
			return -1, fmt.Errorf("git output exceeds %d bytes", r.MaxReadBytes)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return 0, nil
}

type boundedCommandBuffer struct {
	data     []byte
	limit    int64
	exceeded bool
}

func (b *boundedCommandBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - int64(len(b.data))
	if remaining > 0 {
		if int64(n) > remaining {
			p = p[:remaining]
			b.exceeded = true
		}
		b.data = append(b.data, p...)
	} else if n > 0 {
		b.exceeded = true
	}
	return n, nil
}

func (b *boundedCommandBuffer) String() string { return string(b.data) }
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
