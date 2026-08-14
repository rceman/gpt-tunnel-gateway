package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type integrationHookResult struct {
	SourceHead string
	Evidence   string
	Configured bool
}

type integrationHookOutput struct {
	Phase     string          `json:"phase"`
	SourceSHA string          `json:"source_sha"`
	TunnelPID int             `json:"tunnel_pid"`
	Status    json.RawMessage `json:"status"`
}

func runIntegrationHook(ctx context.Context, command model.ProjectGateCommand, root, expectedSource string) (integrationHookResult, error) {
	if len(command.Command) == 0 {
		return integrationHookResult{Evidence: "not_configured"}, nil
	}
	if err := command.Validate("integration hook"); err != nil {
		return integrationHookResult{}, err
	}
	process := exec.CommandContext(ctx, command.Command[0], command.Command[1:]...)
	process.Dir = root
	var stdout, stderr boundedHookOutput
	process.Stdout = &stdout
	process.Stderr = &stderr
	if err := process.Run(); err != nil {
		message := strings.TrimSpace(stdout.String())
		if stderr.String() != "" {
			message = strings.TrimSpace(message + "\n" + stderr.String())
		}
		return integrationHookResult{}, fmt.Errorf("integration hook %q failed: %w: %s", command.Command[0], err, message)
	}
	return parseIntegrationHookEvidence(stdout.String(), expectedSource)
}

func parseIntegrationHookEvidence(evidence, expectedSource string) (integrationHookResult, error) {
	evidence = strings.TrimSpace(evidence)
	if evidence == "not_configured" {
		return integrationHookResult{Evidence: evidence}, nil
	}
	var output integrationHookOutput
	decoder := json.NewDecoder(strings.NewReader(evidence))
	if err := decoder.Decode(&output); err != nil {
		return integrationHookResult{}, fmt.Errorf("integration hook returned invalid JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return integrationHookResult{}, fmt.Errorf("integration hook returned multiple JSON values")
		}
		return integrationHookResult{}, fmt.Errorf("integration hook returned trailing data: %w", err)
	}
	if model.ValidateCommitSHA(output.SourceSHA) != nil || output.SourceSHA != expectedSource {
		return integrationHookResult{}, fmt.Errorf("integration hook source_sha %q does not match expected source %q", output.SourceSHA, expectedSource)
	}
	return integrationHookResult{
		SourceHead: output.SourceSHA,
		Evidence:   evidence,
		Configured: true,
	}, nil
}

func parseConfiguredIntegrationHookEvidence(evidence, expectedSource string) (integrationHookResult, error) {
	if strings.TrimSpace(evidence) == "not_configured" {
		return integrationHookResult{}, fmt.Errorf("configured integration hook returned no source evidence")
	}
	return parseIntegrationHookEvidence(evidence, expectedSource)
}

type boundedHookOutput struct{ data []byte }

func (b *boundedHookOutput) Write(p []byte) (int, error) {
	const maxBytes = 16 << 10
	remaining := maxBytes - len(b.data)
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.data = append(b.data, p...)
	}
	return len(p), nil
}

func (b *boundedHookOutput) String() string { return string(b.data) }
