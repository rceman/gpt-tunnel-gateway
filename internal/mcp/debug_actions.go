package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	debugdomain "github.com/rceman/gpt-tunnel-gateway/internal/debug"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

const (
	gatewaySourceProjectID = "gpt-tunnel-gateway"
	debugTailDefaultLines  = 20
	debugTailMaxLines      = 100
)

var debugActivationAcceptFn = func(c config.Config, configPath, sourceHead string, release func(func())) (debugdomain.ActivationResult, error) {
	return debugdomain.AcceptActivation(c, configPath, sourceHead, release)
}

func (s *Server) ensureDebugActions() {
	if s.Service == nil || !s.Service.Config.Debug.Enabled {
		return
	}
	s.debugActions.Do(func() {
		s.debugActionErr = s.registerDebugActions()
	})
	if s.debugActionErr != nil {
		panic(s.debugActionErr)
	}
}

func (s *Server) registerDebugActions() error {
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "debug/status",
		Description:  "Read bounded host-local source and recovery status.",
		InputSchema:  debugStatusInputSchema(),
		OutputSchema: debugStatusOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    durableSession.RolePlanner,
		LocalReadOnly:    true,
		LocalReceiptOnly: true,
		SessionBound:     true,
		SessionRequired:  true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			if _, err := decodeDebugEmptyInput(raw); err != nil {
				return nil, err
			}
			project, err := configuredGatewaySourceProject(s.Service.Config)
			if err != nil {
				return nil, err
			}
			return debugdomain.Status(ctx, s.Service.Config, s.Service.ConfigPath, project), nil
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "debug/prompt",
		Description:  "Send one bounded direct prompt to a server-selected Agent.",
		InputSchema:  debugPromptInputSchema(),
		OutputSchema: debugPromptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		AuthorityRole:    durableSession.RolePlanner,
		LocalReceiptOnly: true,
		SessionBound:     true,
		SessionRequired:  true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				Agent   string `json:"agent"`
				Message string `json:"message"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			if err := validateCanonicalAgentMessage(in.Message); err != nil {
				return nil, err
			}
			projectID, err := s.boundAgentProject(ctx)
			if err != nil {
				return nil, err
			}
			target, err := s.resolveDebugAgent(projectID, in.Agent)
			if err != nil {
				return nil, err
			}
			result, err := s.Service.Airelay.Prompt(ctx, target.Resolved.SessionKey, in.Message)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"status":    "accepted",
				"agent":     target.Agent.AgentID,
				"exit_code": result.ExitCode,
			}, nil
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "debug/tail",
		Description:  "Read a bounded transcript tail from a server-selected Agent.",
		InputSchema:  debugTailInputSchema(),
		OutputSchema: debugTailOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    durableSession.RolePlanner,
		LocalReadOnly:    true,
		LocalReceiptOnly: true,
		SessionBound:     true,
		SessionRequired:  true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				Agent string `json:"agent"`
				Lines *int   `json:"lines"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			lines := debugTailDefaultLines
			if in.Lines != nil {
				lines = *in.Lines
			}
			if lines < 1 || lines > debugTailMaxLines {
				return nil, fmt.Errorf("debug tail line count must be between 1 and %d", debugTailMaxLines)
			}
			projectID, err := s.boundAgentProject(ctx)
			if err != nil {
				return nil, err
			}
			target, err := s.resolveDebugAgent(projectID, in.Agent)
			if err != nil {
				return nil, err
			}
			result, err := s.Service.Airelay.Tail(ctx, target.Resolved.SessionKey, lines)
			if err != nil {
				return nil, err
			}
			transcript := strings.TrimRight(result.Stdout, "\r\n")
			transcriptLines := []string{}
			if transcript != "" {
				transcriptLines = strings.Split(transcript, "\n")
				if len(transcriptLines) > lines {
					transcriptLines = transcriptLines[len(transcriptLines)-lines:]
				}
			}
			return map[string]any{
				"status":    "ok",
				"agent":     target.Agent.AgentID,
				"lines":     transcriptLines,
				"exit_code": result.ExitCode,
			}, nil
		},
	}); err != nil {
		return err
	}
	return s.RegisterGenericAction(GenericAction{
		Path:         "debug/activate",
		Description:  "Activate one exact clean main source revision through the Gateway-only recovery pipeline.",
		InputSchema:  debugActivateInputSchema(),
		OutputSchema: debugActivateOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole:    durableSession.RolePlanner,
		LocalReceiptOnly: true,
		SessionBound:     true,
		SessionRequired:  true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				MainSHA string `json:"main_sha"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			_, err := configuredGatewaySourceProject(s.Service.Config)
			if err != nil {
				return nil, err
			}
			release, ok := responseReleaseFromContext(ctx)
			if !ok {
				return nil, fmt.Errorf("debug activation requires an HTTP response release boundary")
			}
			return debugActivationAcceptFn(s.Service.Config, s.Service.ConfigPath, in.MainSHA, release)
		},
	})
}

func configuredGatewaySourceProject(c config.Config) (config.ProjectConfig, error) {
	project, ok := c.Projects[gatewaySourceProjectID]
	if !ok || project.Root == "" {
		return config.ProjectConfig{}, fmt.Errorf("configured Gateway source project %q is unavailable", gatewaySourceProjectID)
	}
	return project, nil
}

func decodeDebugEmptyInput(raw json.RawMessage) (struct{}, error) {
	var in struct{}
	if err := decode(raw, &in); err != nil {
		return in, err
	}
	return in, nil
}

func debugStatusInputSchema() map[string]any { return obj(map[string]any{}) }

func debugPromptInputSchema() map[string]any {
	message := str("Bounded direct Agent prompt message.")
	message["minLength"], message["maxLength"] = 1, airelay.MaxPromptBytes
	return obj(map[string]any{
		"agent":   canonicalAgentSelectorSchema(),
		"message": message,
	}, "message")
}

func debugTailInputSchema() map[string]any {
	lines := integer("Maximum transcript lines to return.", 1, debugTailMaxLines)
	lines["default"] = debugTailDefaultLines
	return obj(map[string]any{
		"agent": canonicalAgentSelectorSchema(),
		"lines": lines,
	})
}

func debugTailOutputSchema() map[string]any {
	line := outputString()
	line["maxLength"] = airelay.MaxTransportMessageBytes
	lines := outputArray(line)
	lines["maxItems"] = debugTailMaxLines
	return closedOutput(map[string]any{
		"status":    outputEnum("ok"),
		"agent":     outputString(),
		"lines":     lines,
		"exit_code": integer("Direct Agent exit code.", -1, 1<<31-1),
	}, "status", "agent", "lines", "exit_code")
}

func debugActivateInputSchema() map[string]any {
	mainSHA := str("Exact 40-hex source HEAD on the configured main branch.")
	mainSHA["minLength"], mainSHA["maxLength"] = 40, 40
	mainSHA["pattern"] = "^[0-9a-f]{40}$"
	return obj(map[string]any{"main_sha": mainSHA}, "main_sha")
}

func debugStatusOutputSchema() map[string]any {
	source := closedOutput(map[string]any{
		"root": outputString(), "branch": outputString(), "head": outputString(), "clean": outputBoolean(), "error": outputString(),
	}, "root", "branch", "head", "clean")
	return closedOutput(map[string]any{
		"gateway_id":    outputString(),
		"debug_enabled": outputBoolean(),
		"source":        source,
		"runtime":       debugRuntimeOutputSchema(),
	}, "gateway_id", "debug_enabled", "source", "runtime")
}

func debugRuntimeOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"gateway_pid":                       outputInteger(),
		"running_executable_path":           outputString(),
		"running_executable_sha256":         outputString(),
		"installed_gateway_sha256":          outputString(),
		"installed_cli_sha256":              outputString(),
		"installed_ctl_sha256":              outputString(),
		"installed_artifact_versions":       map[string]any{"type": "object", "additionalProperties": true},
		"artifact_set_coherent":             outputBoolean(),
		"running_gateway_matches_installed": outputBoolean(),
		"installed_version":                 outputString(),
		"running_version":                   outputString(),
		"version_match":                     outputBoolean(),
		"gateway_ready":                     outputBoolean(),
		"tunnel_pid":                        outputInteger(),
		"tunnel_ready":                      outputBoolean(),
		"source_sha":                        outputString(),
		"source_provenance_available":       outputBoolean(),
		"exact_source_match":                outputBoolean(),
		"provenance_reason":                 outputString(),
	}, "artifact_set_coherent", "running_gateway_matches_installed", "version_match", "gateway_ready", "tunnel_ready", "source_provenance_available", "exact_source_match")
}

func debugPromptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"status": outputEnum("accepted"), "agent": outputString(), "exit_code": integer("Direct Agent exit code.", -1, 1<<31-1),
	}, "status", "agent", "exit_code")
}

func debugActivateOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "source_head": outputString(), "activation": outputString(), "smoke": outputString(), "tunnel_pid": integer("Preserved Tunnel PID.", 0, 1<<31-1), "gateway_pid": integer("Activated Gateway PID.", 0, 1<<31-1), "outcome": outputString(),
	}, "operation_id", "source_head", "activation", "smoke", "outcome")
}
