package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/activation"
	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

const gatewaySourceProjectID = "gpt-tunnel-gateway"

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
		Path:             "debug/status",
		Description:      "Read bounded host-local source and runtime recovery status.",
		InputSchema:      debugStatusInputSchema(),
		OutputSchema:     debugStatusOutputSchema(),
		Annotations:      ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		LocalReadOnly:    true,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			if _, err := decodeDebugEmptyInput(raw); err != nil {
				return nil, err
			}
			project, err := configuredGatewaySourceProject(s.Service.Config)
			if err != nil {
				return nil, err
			}
			return activation.DebugStatus(ctx, s.Service.Config, s.Service.ConfigPath, project), nil
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:             "debug/prompt",
		Description:      "Send one bounded direct prompt to a validated Airelay session.",
		InputSchema:      debugPromptInputSchema(),
		OutputSchema:     debugPromptOutputSchema(),
		Annotations:      ToolAnnotations{DestructiveHint: true, IdempotentHint: false},
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				AirelaySession string `json:"airelay_session"`
				Message        string `json:"message"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			result, err := s.Service.Airelay.Prompt(ctx, in.AirelaySession, in.Message)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"status":          "accepted",
				"airelay_session": in.AirelaySession,
				"exit_code":       result.ExitCode,
			}, nil
		},
	}); err != nil {
		return err
	}
	return s.RegisterGenericAction(GenericAction{
		Path:             "debug/activate",
		Description:      "Activate one exact clean main source revision through the Gateway-only recovery pipeline.",
		InputSchema:      debugActivateInputSchema(),
		OutputSchema:     debugActivateOutputSchema(),
		Annotations:      ToolAnnotations{DestructiveHint: true, IdempotentHint: false},
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				MainSHA string `json:"main_sha"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			project, err := configuredGatewaySourceProject(s.Service.Config)
			if err != nil {
				return nil, err
			}
			return activation.DebugActivate(ctx, s.Service.Config, s.Service.ConfigPath, project, in.MainSHA)
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
	session := str("Validated direct Airelay session key.")
	session["minLength"], session["maxLength"] = 1, 128
	session["pattern"] = "^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$"
	message := str("Bounded direct Airelay prompt message.")
	message["minLength"], message["maxLength"] = 1, airelay.MaxPromptBytes
	return obj(map[string]any{
		"airelay_session": session,
		"message":         message,
	}, "airelay_session", "message")
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
		"debug_enabled": outputBoolean(),
		"source":        source,
		"runtime":       debugRuntimeOutputSchema(),
	}, "debug_enabled", "source", "runtime")
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
		"status": outputEnum("accepted"), "airelay_session": outputString(), "exit_code": integer("Direct Airelay exit code.", -1, 1<<31-1),
	}, "status", "airelay_session", "exit_code")
}

func debugActivateOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"source_head": outputString(), "activation": outputString(), "smoke": outputString(), "tunnel_pid": integer("Preserved Tunnel PID.", 0, 1<<31-1), "gateway_pid": integer("Activated Gateway PID.", 0, 1<<31-1),
	}, "source_head", "activation", "smoke")
}
