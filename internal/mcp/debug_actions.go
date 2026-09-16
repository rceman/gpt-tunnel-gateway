package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	debugdomain "github.com/rceman/gpt-tunnel-gateway/internal/debug"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const (
	gatewaySourceProjectID    = "gpt-tunnel-gateway"
	debugTailDefaultLines     = 20
	debugTailMaxLines         = 100
	debugAwaitFinalReadBudget = time.Second
	debugAwaitMaxSeconds      = 600
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
		AuthorityRole:    actionRolePlannerOrLead,
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
		Description:  "Send one bounded prompt directly to an explicit debug Agent reference.",
		InputSchema:  debugPromptInputSchema(),
		OutputSchema: debugPromptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		AuthorityRole:    actionRolePlannerOrLead,
		LocalReceiptOnly: true,
		SessionBound:     true,
		SessionRequired:  true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				AgentRef string `json:"agent_ref"`
				Message  string `json:"message"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			if err := validateDebugAgentRef(in.AgentRef); err != nil {
				return nil, err
			}
			if err := validateCanonicalAgentMessage(in.Message); err != nil {
				return nil, err
			}
			result, err := s.Service.Airelay.Prompt(ctx, in.AgentRef, in.Message)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"status":    "accepted",
				"agent_ref": in.AgentRef,
				"exit_code": result.ExitCode,
			}, nil
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "debug/tail",
		Description:  "Read a bounded transcript tail from an explicit debug Agent reference.",
		InputSchema:  debugTailInputSchema(),
		OutputSchema: debugTailOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    actionRolePlannerOrLead,
		LocalReadOnly:    true,
		LocalReceiptOnly: true,
		SessionBound:     true,
		SessionRequired:  true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				AgentRef string `json:"agent_ref"`
				Lines    *int   `json:"lines"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			if err := validateDebugAgentRef(in.AgentRef); err != nil {
				return nil, err
			}
			lines := debugTailDefaultLines
			if in.Lines != nil {
				lines = *in.Lines
			}
			if lines < 1 || lines > debugTailMaxLines {
				return nil, fmt.Errorf("debug tail line count must be between 1 and %d", debugTailMaxLines)
			}
			result, err := s.Service.Airelay.Tail(ctx, in.AgentRef, lines)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"status":    "ok",
				"agent_ref": in.AgentRef,
				"lines":     debugTranscriptLines(result.Stdout, lines),
				"exit_code": result.ExitCode,
			}, nil
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "debug/await",
		Description:  "Supervise an explicit debug Agent reference for a bounded interval.",
		InputSchema:  debugAwaitInputSchema(),
		OutputSchema: debugAwaitOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    actionRolePlannerOrLead,
		LocalReadOnly:    true,
		LocalReceiptOnly: true,
		SessionBound:     true,
		SessionRequired:  true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			return s.debugAwaitAction(ctx, raw)
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
		AuthorityRole:    actionRolePlannerOrLead,
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

func debugAgentRefSchema() map[string]any {
	ref := str("Direct existing Airelay Agent reference for break-glass recovery.")
	ref["minLength"], ref["maxLength"] = 1, 128
	ref["pattern"] = `^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`
	return ref
}

func validateDebugAgentRef(ref string) error {
	if err := model.ValidateObjectIdentifier(ref); err != nil {
		return fmt.Errorf("invalid debug agent_ref")
	}
	return nil
}

func debugTranscriptLines(stdout string, lines int) []string {
	transcript := strings.TrimRight(stdout, "\x0d\x0a")
	if transcript == "" {
		return []string{}
	}
	result := strings.Split(transcript, "\n")
	if len(result) > lines {
		result = result[len(result)-lines:]
	}
	return result
}

func debugPromptInputSchema() map[string]any {
	message := str("Bounded direct Agent prompt message.")
	message["minLength"], message["maxLength"] = 1, airelay.MaxPromptBytes
	return obj(map[string]any{
		"agent_ref": debugAgentRefSchema(),
		"message":   message,
	}, "agent_ref", "message")
}

func debugTailInputSchema() map[string]any {
	lines := integer("Maximum transcript lines to return.", 1, debugTailMaxLines)
	lines["default"] = debugTailDefaultLines
	return obj(map[string]any{
		"agent_ref": debugAgentRefSchema(),
		"lines":     lines,
	}, "agent_ref")
}

func debugAwaitInputSchema() map[string]any {
	seconds := integer("Seconds to supervise a direct Agent reference.", 1, debugAwaitMaxSeconds)
	lines := integer("Maximum transcript lines to return.", 1, debugTailMaxLines)
	lines["default"] = debugTailDefaultLines
	return obj(map[string]any{
		"agent_ref": debugAgentRefSchema(),
		"seconds":   seconds,
		"lines":     lines,
	}, "agent_ref", "seconds")
}

func debugTailOutputSchema() map[string]any {
	line := outputString()
	line["maxLength"] = airelay.MaxTransportMessageBytes
	lines := outputArray(line)
	lines["maxItems"] = debugTailMaxLines
	return closedOutput(map[string]any{
		"status":    outputEnum("ok"),
		"agent_ref": outputString(),
		"lines":     lines,
		"exit_code": integer("Direct Agent exit code.", -1, 1<<31-1),
	}, "status", "agent_ref", "lines", "exit_code")
}

func debugAwaitOutputSchema() map[string]any {
	line := outputString()
	line["maxLength"] = airelay.MaxTransportMessageBytes
	lines := outputArray(line)
	lines["maxItems"] = debugTailMaxLines
	return closedOutput(map[string]any{
		"status":               outputEnum("idle", "running", "waiting", "error"),
		"agent_ref":            outputString(),
		"controller_reachable": outputBoolean(),
		"lines":                lines,
		"exit_code":            integer("Direct Agent exit code.", -1, 1<<31-1),
	}, "status", "agent_ref", "controller_reachable", "lines", "exit_code")
}

func (s *Server) debugAwaitAction(ctx context.Context, raw json.RawMessage) (any, error) {
	actionStarted := time.Now()
	var in struct {
		AgentRef string `json:"agent_ref"`
		Seconds  int    `json:"seconds"`
		Lines    *int   `json:"lines"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := validateDebugAgentRef(in.AgentRef); err != nil {
		return nil, err
	}
	seconds := in.Seconds
	if seconds < 1 || seconds > debugAwaitMaxSeconds {
		return nil, fmt.Errorf("debug await seconds must be between 1 and %d", debugAwaitMaxSeconds)
	}
	lines := debugTailDefaultLines
	if in.Lines != nil {
		lines = *in.Lines
	}
	if lines < 1 || lines > debugTailMaxLines {
		return nil, fmt.Errorf("debug await line count must be between 1 and %d", debugTailMaxLines)
	}
	awaitDeadline := actionStarted.Add(time.Duration(seconds) * time.Second)
	awaitCtx, cancel := context.WithDeadline(ctx, awaitDeadline)
	defer cancel()
	finalReadAt := awaitDeadline.Add(-debugAwaitFinalReadBudget)
	if wait := time.Until(finalReadAt); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-awaitCtx.Done():
			return nil, awaitCtx.Err()
		}
	} else if err := ctx.Err(); err != nil {
		return nil, err
	}
	status, err := s.Service.Airelay.Status(awaitCtx, in.AgentRef)
	if err != nil {
		return nil, err
	}
	tail, err := s.Service.Airelay.Tail(awaitCtx, in.AgentRef, lines)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":               status.State,
		"agent_ref":            in.AgentRef,
		"controller_reachable": status.ControllerReachable,
		"lines":                debugTranscriptLines(tail.Stdout, lines),
		"exit_code":            tail.ExitCode,
	}, nil
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
		"status": outputEnum("accepted"), "agent_ref": outputString(), "exit_code": integer("Direct Agent exit code.", -1, 1<<31-1),
	}, "status", "agent_ref", "exit_code")
}

func debugActivateOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "source_head": outputString(), "activation": outputString(), "smoke": outputString(), "tunnel_pid": integer("Preserved Tunnel PID.", 0, 1<<31-1), "gateway_pid": integer("Activated Gateway PID.", 0, 1<<31-1), "outcome": outputString(),
	}, "operation_id", "source_head", "activation", "smoke", "outcome")
}
