package airelay

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var sessionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

type Result struct {
	ExitCode   int       `json:"exit_code"`
	Stdout     string    `json:"stdout"`
	Stderr     string    `json:"stderr"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

type Client struct {
	Command         string
	Timeout         time.Duration
	MaxMessageBytes int
}

func (c Client) Prompt(ctx context.Context, session, message string) (Result, error) {
	if !sessionRE.MatchString(session) {
		return Result{}, fmt.Errorf("invalid Airelay session key")
	}
	if message == "" || len(message) > c.MaxMessageBytes || strings.ContainsRune(message, 0) {
		return Result{}, fmt.Errorf("invalid Airelay message")
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	result := Result{StartedAt: time.Now().UTC()}
	cmd := exec.CommandContext(ctx, c.Command, "prompt", session, message)
	cmd.Env = cleanEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result.FinishedAt = time.Now().UTC()
	result.Stdout = bounded(stdout.String(), 8192)
	result.Stderr = bounded(stderr.String(), 8192)
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		result.ExitCode = -1
	}
	if ctx.Err() != nil {
		return result, fmt.Errorf("Airelay prompt timeout: %w", ctx.Err())
	}
	if err != nil {
		return result, fmt.Errorf("Airelay prompt failed: %w", err)
	}
	return result, nil
}
func cleanEnv() []string {
	keys := []string{"HOME", "PATH", "USER", "LOGNAME", "TMPDIR"}
	out := []string{"LC_ALL=C"}
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}
func bounded(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n[truncated]"
}
