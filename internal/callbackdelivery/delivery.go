// Package callbackdelivery owns the bounded external mechanics for project
// callbacks. Service code supplies validated definitions and never handles
// URLs, filesystem paths, or processes directly.
package callbackdelivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const (
	MaxOutputBytes   = 16 << 10
	MaxResponseBytes = 4 << 10
	maxDeliveryTime  = 5 * time.Second
)

func Deliver(ctx context.Context, callback model.ProjectCallback, project config.ProjectConfig, git gitx.Runner, eventPayload []byte) error {
	if err := model.ValidateProjectCallback(callback); err != nil {
		return err
	}
	var wait sync.WaitGroup
	errorsCh := make(chan error, 2)
	if callback.URL != nil {
		definition := *callback.URL
		wait.Add(1)
		go func() {
			defer wait.Done()
			deliveryCtx, cancel := context.WithTimeout(ctx, maxDeliveryTime)
			defer cancel()
			if err := deliverHTTP(deliveryCtx, definition); err != nil {
				errorsCh <- fmt.Errorf("callback %q HTTP delivery: %w", callback.Callback, err)
			}
		}()
	}
	if callback.Script != nil {
		definition := *callback.Script
		wait.Add(1)
		go func() {
			defer wait.Done()
			deliveryCtx, cancel := context.WithTimeout(ctx, maxDeliveryTime)
			defer cancel()
			if err := deliverScript(deliveryCtx, definition, project, git, eventPayload); err != nil {
				errorsCh <- fmt.Errorf("callback %q script delivery: %w", callback.Callback, err)
			}
		}()
	}
	if callback.URL == nil && callback.Script == nil {
		return fmt.Errorf("callback %q has no delivery target", callback.Callback)
	}
	wait.Wait()
	close(errorsCh)
	var deliveryErr error
	for err := range errorsCh {
		deliveryErr = errors.Join(deliveryErr, err)
	}
	return deliveryErr
}

func deliverHTTP(ctx context.Context, definition model.ProjectCallbackURL) error {
	request, err := http.NewRequestWithContext(ctx, definition.Method, definition.URL, strings.NewReader(definition.Body))
	if err != nil {
		return err
	}
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, MaxResponseBytes+1))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("HTTP status %d", response.StatusCode)
	}
	return nil
}

func deliverScript(ctx context.Context, definition model.ProjectCallbackScript, project config.ProjectConfig, git gitx.Runner, eventPayload []byte) error {
	status, err := git.WorktreeStatus(ctx, project)
	if err != nil {
		return fmt.Errorf("inspect canonical project checkout: %w", err)
	}
	if !status.Clean {
		return fmt.Errorf("canonical project checkout is dirty")
	}
	if project.DefaultBranch == "" || status.Branch != project.DefaultBranch {
		return fmt.Errorf("canonical project checkout is not on the configured integration branch")
	}
	root, script, err := resolveScript(project.Root, definition.Path)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, script, definition.Args...)
	command.Dir = root
	command.Env = callbackEnvironment()
	command.Stdin = bytes.NewReader(eventPayload)
	stdout := &boundedBuffer{limit: MaxOutputBytes}
	stderr := &boundedBuffer{limit: MaxOutputBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if stdout.exceeded || stderr.exceeded {
			return fmt.Errorf("script output exceeds %d bytes", MaxOutputBytes)
		}
		if ctx.Err() != nil {
			return fmt.Errorf("script timeout: %w", ctx.Err())
		}
		return fmt.Errorf("script failed: %w", err)
	}
	if stdout.exceeded || stderr.exceeded {
		return fmt.Errorf("script output exceeds %d bytes", MaxOutputBytes)
	}
	return nil
}

func resolveScript(root, path string) (string, string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", "", fmt.Errorf("canonical project checkout root is invalid")
	}
	resolvedRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", "", fmt.Errorf("resolve canonical project checkout: %w", err)
	}
	candidate := filepath.Join(resolvedRoot, filepath.FromSlash(path))
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", "", fmt.Errorf("resolve callback script: %w", err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", fmt.Errorf("callback script escapes canonical project checkout")
	}
	info, err := os.Stat(resolvedCandidate)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("callback script is not a regular file")
	}
	return resolvedRoot, resolvedCandidate, nil
}

func callbackEnvironment() []string {
	env := []string{"LC_ALL=C"}
	for _, key := range []string{"HOME", "PATH", "TMPDIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

type boundedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		b.exceeded = true
		return 0, fmt.Errorf("bounded output exceeded")
	}
	return b.Buffer.Write(p)
}

// ReadFrom prevents os/exec's io.Copy fast path from bypassing the bounded
// Write method promoted by bytes.Buffer.
func (b *boundedBuffer) ReadFrom(r io.Reader) (int64, error) {
	var buffer [32 << 10]byte
	var total int64
	for {
		n, err := r.Read(buffer[:])
		if n > 0 {
			total += int64(n)
			if _, writeErr := b.Write(buffer[:n]); writeErr != nil {
				return total, writeErr
			}
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}
