package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/actioncontract"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

const (
	procedureExecutionKind      = "procedure-execution"
	maxProcedureInputBytes      = 64 << 10
	maxProcedureOutputBytes     = 64 << 10
	maxProcedureLogBytes        = 16 << 10
	procedureExecutionTimeout   = 30 * time.Minute
	procedureExecutionOperation = "procedure-execution/v1"
)

type ProcedureExecutionStartInput struct {
	ProjectID             string
	Name                  string
	Definition            model.ProjectProcedureDefinition
	ConfigurationRevision int
	Input                 json.RawMessage
}

type ProcedureExecutionReceipt struct {
	Operation string `json:"operation"`
	Status    string `json:"status"`
}

type ProcedureInputEnvelope struct {
	Input   json.RawMessage           `json:"input"`
	Context ProcedureExecutionContext `json:"context"`
}

type ProcedureExecutionContext struct {
	Project               string         `json:"project"`
	Operation             string         `json:"operation"`
	Session               string         `json:"session"`
	Hook                  string         `json:"hook,omitempty"`
	ConfigurationRevision int            `json:"configuration_revision"`
	ResolvedReferences    map[string]any `json:"resolved_references"`
}

type procedureExecutionRequest struct {
	Version               string                           `json:"version"`
	ProjectID             string                           `json:"project_id"`
	Name                  string                           `json:"name"`
	ConfigurationRevision int                              `json:"configuration_revision"`
	Definition            model.ProjectProcedureDefinition `json:"definition"`
	Input                 json.RawMessage                  `json:"input"`
	SessionID             string                           `json:"session_id"`
	Hook                  string                           `json:"hook,omitempty"`
	ParentOperationID     string                           `json:"parent_operation_id,omitempty"`
	ExecutionRoot         string                           `json:"execution_root"`
}

type procedureExecutionError struct {
	Reason         string
	OutcomeUnknown bool
}

func (e *procedureExecutionError) Error() string { return e.Reason }

func (s *Service) ProcedureExecutionStart(ctx context.Context, in ProcedureExecutionStartInput) (ProcedureExecutionReceipt, error) {
	if AgentSessionID(ctx) == "" {
		return ProcedureExecutionReceipt{}, fmt.Errorf("Procedure calls require a bound Session")
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return ProcedureExecutionReceipt{}, err
	}
	if err := model.ValidateProcedureName(in.Name); err != nil {
		return ProcedureExecutionReceipt{}, err
	}
	configuration, err := s.ProjectConfigurationRead(ctx, in.ProjectID)
	if err != nil {
		return ProcedureExecutionReceipt{}, err
	}
	definition, exists := configuration.Procedures[in.Name]
	if !exists || configuration.Revision != in.ConfigurationRevision || !reflect.DeepEqual(definition, in.Definition) {
		return ProcedureExecutionReceipt{}, fmt.Errorf("configured Procedure changed before admission")
	}
	project, err := s.EffectiveProjectConfig(in.ProjectID)
	if err != nil {
		return ProcedureExecutionReceipt{}, err
	}
	if _, _, err := resolveProcedureScript(project.Root, definition.Script); err != nil {
		return ProcedureExecutionReceipt{}, fmt.Errorf("Procedure script is unavailable: %w", err)
	}
	if len(in.Input) == 0 || len(in.Input) > maxProcedureInputBytes {
		return ProcedureExecutionReceipt{}, fmt.Errorf("Procedure input exceeds bounds")
	}
	compiled, err := compileProcedureContract(in.Name, definition)
	if err != nil {
		return ProcedureExecutionReceipt{}, err
	}
	value, err := decodeProcedureJSON(in.Input)
	if err != nil {
		return ProcedureExecutionReceipt{}, fmt.Errorf("Procedure input is invalid JSON")
	}
	if err := actioncontract.ValidateCompiledActionInput(compiled, value); err != nil {
		return ProcedureExecutionReceipt{}, fmt.Errorf("Procedure input does not match its contract: %w", err)
	}
	if _, err := s.resolveProcedureReferences(ctx, in.ProjectID, AgentSessionID(ctx), "", definition.Input, value); err != nil {
		return ProcedureExecutionReceipt{}, err
	}
	request := procedureExecutionRequest{
		Version:               procedureExecutionOperation,
		ProjectID:             in.ProjectID,
		Name:                  in.Name,
		ConfigurationRevision: configuration.Revision,
		Definition:            definition,
		Input:                 append(json.RawMessage(nil), in.Input...),
		SessionID:             AgentSessionID(ctx),
		ExecutionRoot:         project.Root,
	}
	operation, err := s.allocateProcedureExecution(ctx, request)
	if err != nil {
		return ProcedureExecutionReceipt{}, err
	}
	s.enqueueDurableMutation(operation.OperationID)
	return ProcedureExecutionReceipt{
		Operation: operation.OperationID,
		Status:    operation.Status,
	}, nil
}

func (s *Service) allocateProcedureExecution(ctx context.Context, request procedureExecutionRequest) (durableMutationOperation, error) {
	if s.Durability == nil || s.Durability.Local == nil {
		return durableMutationOperation{}, fmt.Errorf("local durability is unavailable")
	}
	if err := model.ValidateProjectIdentifier(request.ProjectID); err != nil {
		return durableMutationOperation{}, err
	}
	if err := model.ValidateProcedureName(request.Name); err != nil || request.Version != procedureExecutionOperation {
		return durableMutationOperation{}, fmt.Errorf("invalid Procedure execution request")
	}
	if len(request.Input) == 0 || len(request.Input) > maxProcedureInputBytes || request.ConfigurationRevision < 1 || request.ExecutionRoot == "" || request.SessionID == "" {
		return durableMutationOperation{}, fmt.Errorf("invalid Procedure execution request")
	}
	if request.Hook != "" && !model.IsProjectHookName(request.Hook) || request.ParentOperationID != "" && model.ValidateOperationID(request.ParentOperationID) != nil {
		return durableMutationOperation{}, fmt.Errorf("invalid Procedure execution context")
	}
	configuration, err := s.ProjectConfigurationRead(ctx, request.ProjectID)
	if err != nil || configuration.Revision != request.ConfigurationRevision {
		return durableMutationOperation{}, fmt.Errorf("configured Procedure changed before admission")
	}
	configuredDefinition, found := configuration.Procedures[request.Name]
	if !found || !reflect.DeepEqual(configuredDefinition, request.Definition) || model.ValidateProjectProcedureDefinition(request.Definition) != nil {
		return durableMutationOperation{}, fmt.Errorf("configured Procedure changed before admission")
	}
	project, err := s.EffectiveProjectConfig(request.ProjectID)
	if err != nil || filepath.Clean(project.Root) != filepath.Clean(request.ExecutionRoot) {
		return durableMutationOperation{}, fmt.Errorf("Procedure execution root is not the configured project root")
	}
	s.startDurableMutationWorker()
	raw, err := json.Marshal(request)
	if err != nil {
		return durableMutationOperation{}, err
	}
	projectCode, err := s.localOperationProjectCode(ctx, request.ProjectID)
	if err != nil {
		return durableMutationOperation{}, err
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return durableMutationOperation{}, fmt.Errorf("generate Procedure Operation identity: %w", err)
	}
	identity := append(random[:], raw...)
	digest := sha256.Sum256(identity)
	mutationID := hex.EncodeToString(digest[:])
	now := s.durableNow()
	allocated, err := s.Durability.AllocateLocalOperation(ctx, request.ProjectID, projectCode, mutationID, procedureExecutionKind, now)
	if err != nil {
		return durableMutationOperation{}, err
	}
	operation := durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   allocated.OperationID,
		MutationID:    mutationID,
		Kind:          procedureExecutionKind,
		RequestSHA256: mutationID,
		SessionID:     request.SessionID,
		ProjectID:     request.ProjectID,
		Input:         raw,
		Status:        "accepted",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.writeDurableMutation(operation); err != nil {
		local, readErr := s.Durability.ReadLocalOperation(context.Background(), allocated.OperationID)
		if readErr == nil {
			local.Status = "failed"
			local.Error = "Procedure execution could not be durably admitted"
			local.UpdatedAt = s.durableNow()
			_ = s.Durability.UpdateLocalOperation(context.Background(), local)
		}
		return durableMutationOperation{}, fmt.Errorf("persist Procedure Operation: %w", err)
	}
	return operation, nil
}

func (s *Service) executeProcedureOperation(ctx context.Context, operation durableMutationOperation) (json.RawMessage, error) {
	var request procedureExecutionRequest
	if err := json.Unmarshal(operation.Input, &request); err != nil || request.Version != procedureExecutionOperation || request.ProjectID != operation.ProjectID || request.SessionID != operation.SessionID {
		return nil, &procedureExecutionError{Reason: "Procedure execution request is invalid"}
	}
	if err := validateProcedureOperationRecord(operation, request); err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure Operation record is invalid"}
	}
	configuration, err := s.ProjectConfigurationRead(ctx, request.ProjectID)
	if err != nil || configuration.Revision != request.ConfigurationRevision {
		return nil, &procedureExecutionError{Reason: "Procedure configuration changed before launch"}
	}
	configuredDefinition, found := configuration.Procedures[request.Name]
	if !found || !reflect.DeepEqual(configuredDefinition, request.Definition) {
		return nil, &procedureExecutionError{Reason: "Procedure configuration changed before launch"}
	}
	project, err := s.EffectiveProjectConfig(request.ProjectID)
	if err != nil || filepath.Clean(project.Root) != filepath.Clean(request.ExecutionRoot) {
		return nil, &procedureExecutionError{Reason: "Procedure execution root changed before launch"}
	}
	ctx = procedureRequestContext(ctx, request)
	compiled, err := compileProcedureContract(request.Name, request.Definition)
	if err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure contract is invalid"}
	}
	input, err := decodeProcedureJSON(request.Input)
	if err != nil || actioncontract.ValidateCompiledActionInput(compiled, input) != nil {
		return nil, &procedureExecutionError{Reason: "Procedure input is invalid"}
	}
	if err := validateProcedureContextReferences(ctx, request); err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure references are no longer valid"}
	}
	resolved, err := s.resolveProcedureReferences(ctx, request.ProjectID, request.SessionID, operation.OperationID, request.Definition.Input, input)
	if err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure references are no longer valid"}
	}
	contextOperation := request.ParentOperationID
	if contextOperation == "" {
		contextOperation = operation.OperationID
	}
	procedureContext := ProcedureExecutionContext{
		Project:               request.ProjectID,
		Operation:             contextOperation,
		Session:               request.SessionID,
		Hook:                  request.Hook,
		ConfigurationRevision: request.ConfigurationRevision,
		ResolvedReferences:    resolved,
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure input is invalid"}
	}
	envelope, err := json.Marshal(ProcedureInputEnvelope{
		Input:   inputJSON,
		Context: procedureContext,
	})
	if err != nil || len(envelope) > maxProcedureInputBytes+maxProcedureOutputBytes {
		return nil, &procedureExecutionError{Reason: "Procedure context is invalid"}
	}
	root, script, err := resolveProcedureScript(request.ExecutionRoot, request.Definition.Script)
	if err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure executable is unavailable"}
	}
	runtimeID, err := newProcedureRuntimeID()
	if err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure runtime identity is unavailable"}
	}
	output, err := executeProcedureScript(ctx, s.Config.StateDir, runtimeID, root, script, envelope)
	if err != nil {
		return nil, err
	}
	outputValue, err := decodeProcedureJSON(output)
	if err != nil {
		return nil, &procedureExecutionError{
			Reason:         "Procedure structured output is invalid",
			OutcomeUnknown: true,
		}
	}
	outputAction := compiled
	resultSchema, ok := procedureCompletedResultSchema(compiled.Output)
	if !ok {
		return nil, &procedureExecutionError{Reason: "Procedure output contract is invalid"}
	}
	outputAction.Output = resultSchema
	if err := actioncontract.ValidateCompiledActionOutput(outputAction, outputValue); err != nil {
		return nil, &procedureExecutionError{
			Reason:         "Procedure structured output does not match its contract",
			OutcomeUnknown: true,
		}
	}
	if _, err := s.resolveProcedureReferences(ctx, request.ProjectID, request.SessionID, operation.OperationID, request.Definition.Output, outputValue); err != nil {
		return nil, &procedureExecutionError{
			Reason:         "Procedure output references are invalid",
			OutcomeUnknown: true,
		}
	}
	result, err := json.Marshal(outputValue)
	if err != nil {
		return nil, &procedureExecutionError{
			Reason:         "Procedure structured output is invalid",
			OutcomeUnknown: true,
		}
	}
	return result, nil
}

func procedureCompletedResultSchema(schema *actioncontract.CompiledSchema) (*actioncontract.CompiledSchema, bool) {
	if schema == nil || len(schema.OneOf) == 0 {
		return nil, false
	}
	for _, branch := range schema.OneOf {
		status := branch.Properties["status"]
		if status == nil || len(status.Enum) != 1 || status.Enum[0] != "completed" {
			continue
		}
		result := branch.Properties["result"]
		return result, result != nil
	}
	return nil, false
}

func validateProcedureContextReferences(ctx context.Context, request procedureExecutionRequest) error {
	if request.SessionID == "" || AgentSessionID(ctx) != request.SessionID {
		return fmt.Errorf("Procedure execution Session context does not match admission")
	}
	if request.Hook != "" && !model.IsProjectHookName(request.Hook) {
		return fmt.Errorf("invalid Hook identity")
	}
	if request.ParentOperationID != "" && model.ValidateOperationID(request.ParentOperationID) != nil {
		return fmt.Errorf("invalid parent Operation reference")
	}
	return nil
}

func compileProcedureContract(name string, definition model.ProjectProcedureDefinition) (actioncontract.CompiledAction, error) {
	if err := model.ValidateProcedureName(name); err != nil {
		return actioncontract.CompiledAction{}, err
	}
	if err := model.ValidateProjectProcedureDefinition(definition); err != nil {
		return actioncontract.CompiledAction{}, err
	}
	contracts, err := actioncontract.LoadCanonical()
	if err != nil {
		return actioncontract.CompiledAction{}, err
	}
	return contracts.CompileProcedureAction("procedure/"+name, definition.Summary, definition.Guide, definition.Input, definition.Output)
}

func decodeProcedureJSON(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("unexpected Procedure JSON suffix")
	}
	return value, nil
}

func resolveProcedureScript(root, scriptPath string) (string, string, error) {
	if root == "" || !filepath.IsAbs(root) || scriptPath == "" || filepath.IsAbs(scriptPath) || strings.Contains(scriptPath, "\\") || strings.ContainsAny(scriptPath, "\x00\r\n") {
		return "", "", fmt.Errorf("invalid Procedure script root or path")
	}
	for _, part := range strings.Split(filepath.ToSlash(scriptPath), "/") {
		if part == "" || part == "." || part == ".." {
			return "", "", fmt.Errorf("invalid Procedure script path")
		}
	}
	resolvedRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", "", err
	}
	candidate := filepath.Join(resolvedRoot, filepath.FromSlash(scriptPath))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", "", err
	}
	relative, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", fmt.Errorf("Procedure script escapes its repository root")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", "", fmt.Errorf("Procedure script is not a regular executable file")
	}
	return resolvedRoot, resolved, nil
}

func validProcedureRuntimeID(value string) bool {
	if len(value) != 32 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16 && hex.EncodeToString(decoded) == value
}

func newProcedureRuntimeID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func executeProcedureScript(ctx context.Context, stateDir, runtimeID, root, script string, envelope []byte) ([]byte, error) {
	if stateDir == "" || !validProcedureRuntimeID(runtimeID) {
		return nil, &procedureExecutionError{Reason: "Procedure runtime directory is invalid"}
	}
	dir := filepath.Join(stateDir, "procedure-runs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure runtime directory is unavailable"}
	}
	runDir := filepath.Join(dir, runtimeID)
	if err := os.Mkdir(runDir, 0o700); err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure runtime files are unavailable"}
	}
	defer os.RemoveAll(runDir)
	inputPath := filepath.Join(runDir, "input.json")
	outputPath := filepath.Join(runDir, "output.json")
	if len(envelope) == 0 || len(envelope) > maxProcedureInputBytes+maxProcedureOutputBytes {
		return nil, &procedureExecutionError{Reason: "Procedure input envelope exceeds its bounds"}
	}
	if err := writeProcedurePrivateFile(inputPath, envelope); err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure input envelope could not be prepared"}
	}
	outputFile, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure output file could not be prepared"}
	}
	if err := outputFile.Close(); err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure output file could not be prepared"}
	}
	cmdCtx, cancel := context.WithTimeout(ctx, procedureExecutionTimeout)
	defer cancel()
	command := exec.CommandContext(cmdCtx, script)
	command.Dir = root
	command.Env = procedureExecutionEnvironment(inputPath, outputPath, runDir)
	stdout, stderr := &boundedProcedureBuffer{limit: maxProcedureLogBytes}, &boundedProcedureBuffer{limit: maxProcedureLogBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	runErr := command.Run()
	if stdout.exceeded || stderr.exceeded {
		return nil, &procedureExecutionError{
			Reason:         "Procedure logs exceeded their bounds",
			OutcomeUnknown: true,
		}
	}
	if runErr != nil {
		if cmdCtx.Err() != nil {
			return nil, &procedureExecutionError{
				Reason:         "Procedure execution outcome is unknown after timeout",
				OutcomeUnknown: true,
			}
		}
		var execErr *exec.Error
		if errors.As(runErr, &execErr) {
			return nil, &procedureExecutionError{Reason: "Procedure executable could not be started"}
		}
		return nil, &procedureExecutionError{Reason: "Procedure exited unsuccessfully"}
	}
	info, err := os.Lstat(outputPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxProcedureOutputBytes {
		return nil, &procedureExecutionError{
			Reason:         "Procedure structured output is missing or exceeds its bounds",
			OutcomeUnknown: true,
		}
	}
	result, err := os.ReadFile(outputPath)
	if err != nil || len(result) > maxProcedureOutputBytes {
		return nil, &procedureExecutionError{
			Reason:         "Procedure structured output could not be read",
			OutcomeUnknown: true,
		}
	}
	return result, nil
}

func writeProcedurePrivateFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}

func procedureExecutionEnvironment(inputPath, outputPath, runDir string) []string {
	return []string{
		"LC_ALL=C",
		"PATH=/usr/bin:/bin",
		"TMPDIR=" + runDir,
		"GTW_PROCEDURE_INPUT_FILE=" + inputPath,
		"GTW_PROCEDURE_OUTPUT_FILE=" + outputPath,
	}
}

type boundedProcedureBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *boundedProcedureBuffer) Write(value []byte) (int, error) {
	remaining := b.limit - b.Len()
	if len(value) > remaining {
		b.exceeded = true
		return 0, fmt.Errorf("Procedure log exceeded its bound")
	}
	return b.Buffer.Write(value)
}

func (b *boundedProcedureBuffer) ReadFrom(reader io.Reader) (int64, error) {
	var chunk [32 << 10]byte
	var total int64
	for {
		count, err := reader.Read(chunk[:])
		if count > 0 {
			total += int64(count)
			if _, writeErr := b.Write(chunk[:count]); writeErr != nil {
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

func (s *Service) resolveProcedureReferences(ctx context.Context, projectID, sessionID, operationID string, schema map[string]any, value any) (map[string]any, error) {
	resolved := make(map[string]any)
	var walk func(map[string]any, any, string, map[string]any) error
	walk = func(current map[string]any, currentValue any, path string, siblings map[string]any) error {
		if reference, _ := current["$ref"].(string); reference == "EntityKeyAndReference" {
			name := path
			if index := strings.LastIndex(path, "."); index >= 0 {
				name = path[index+1:]
			}
			key, ok := currentValue.(string)
			if !ok || key == "" {
				return fmt.Errorf("Procedure reference %q must be a string", name)
			}
			entry, err := s.resolveProcedureEntityReference(ctx, projectID, sessionID, operationID, name, key)
			if err != nil {
				return err
			}
			if name == "task" || name == "track" {
				revisionField := name + "_revision"
				if rawRevision, exists := siblings[revisionField]; exists {
					revision, ok := procedureInteger(rawRevision)
					actual, actualOK := entry["revision"].(int)
					if !ok || !actualOK || revision != actual {
						return fmt.Errorf("Procedure %s reference revision is stale", name)
					}
				}
			}
			if _, exists := resolved[name]; exists {
				encodedExisting, _ := json.Marshal(resolved[name])
				encodedNew, _ := json.Marshal(entry)
				if !bytes.Equal(encodedExisting, encodedNew) {
					return fmt.Errorf("Procedure reference field %q is ambiguous", name)
				}
			} else {
				resolved[name] = entry
			}
			return nil
		}
		typ, _ := current["type"].(string)
		switch typ {
		case "object":
			properties, _ := current["properties"].(map[string]any)
			object, ok := currentValue.(map[string]any)
			if !ok {
				return nil
			}
			for name, child := range properties {
				childSchema, ok := child.(map[string]any)
				if !ok {
					continue
				}
				childValue, present := object[name]
				if !present {
					continue
				}
				childPath := name
				if path != "" {
					childPath = path + "." + name
				}
				if err := walk(childSchema, childValue, childPath, object); err != nil {
					return err
				}
			}
		case "array":
			items, _ := current["items"].(map[string]any)
			values, _ := currentValue.([]any)
			for index, item := range values {
				if err := walk(items, item, fmt.Sprintf("%s[%d]", path, index), nil); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(schema, value, "", nil); err != nil {
		return nil, err
	}
	return resolved, nil
}

func (s *Service) resolveProcedureEntityReference(ctx context.Context, projectID, sessionID, operationID, name, key string) (map[string]any, error) {
	result := map[string]any{"key": key}
	switch name {
	case "project":
		if key != projectID || model.ValidateProjectIdentifier(key) != nil {
			return nil, fmt.Errorf("Procedure project reference is outside the bound project")
		}
	case "task":
		task, err := s.TaskAuthoringRead(ctx, projectID, key)
		if err != nil || task.ProjectID != projectID || task.ID != key {
			return nil, fmt.Errorf("Procedure Task reference does not resolve in the bound project")
		}
		result["revision"] = task.Revision
	case "track":
		track, err := s.TrackLifecycleRead(ctx, projectID, key, 0)
		if err != nil || track.ProjectID != projectID || track.ID != key {
			return nil, fmt.Errorf("Procedure Track reference does not resolve in the bound project")
		}
		result["revision"] = track.Revision
	case "operation":
		if s.Durability == nil {
			return nil, fmt.Errorf("Procedure Operation reference requires Local durability")
		}
		operation, err := s.Durability.ReadLocalOperation(ctx, key)
		if err != nil || operation.ProjectID != projectID {
			return nil, fmt.Errorf("Procedure Operation reference does not resolve in the bound project")
		}
		if operationID != "" && key == operationID && operation.Status == "" {
			return nil, fmt.Errorf("Procedure Operation reference is incomplete")
		}
		result["status"] = operation.Status
	case "session":
		if sessionID == "" || key != sessionID || s.Durability == nil {
			return nil, fmt.Errorf("Procedure Session reference must be the triggering Session")
		}
		record, err := durableSession.NewStoreWithDurability(s.Durability).Get(key)
		if err != nil || record.ProjectID != projectID {
			return nil, fmt.Errorf("Procedure Session reference does not resolve in the bound project")
		}
	case "agent":
		agent, err := s.AgentRead(ctx, projectID, key)
		if err != nil || agent.ProjectID != projectID || agent.AgentID != key {
			return nil, fmt.Errorf("Procedure Agent reference does not resolve in the bound project")
		}
		result["role"] = agent.Role
		result["enabled"] = agent.Enabled
	default:
		return nil, fmt.Errorf("unsupported Procedure reference field %q", name)
	}
	return result, nil
}

func procedureInteger(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case int64:
		return int(number), int64(int(number)) == number
	case json.Number:
		parsed, err := number.Int64()
		return int(parsed), err == nil && int64(int(parsed)) == parsed
	case float64:
		return int(number), number == float64(int(number))
	default:
		return 0, false
	}
}

func procedureRequestContext(ctx context.Context, request procedureExecutionRequest) context.Context {
	if request.SessionID != "" {
		ctx = WithAgentSessionID(ctx, request.SessionID)
	}
	return ctx
}

func validateProcedureOperationRecord(operation durableMutationOperation, request procedureExecutionRequest) error {
	if operation.Kind != procedureExecutionKind || operation.ProjectID != request.ProjectID || operation.SessionID != request.SessionID || model.ValidateOperationID(operation.OperationID) != nil {
		return fmt.Errorf("Procedure Operation record does not match its request")
	}
	return nil
}

func procedureOperationResult(operation durableMutationOperation) map[string]any {
	result := map[string]any{"operation": operation.OperationID, "status": operation.Status}
	if operation.Status == "failed" || operation.Status == "outcome_unknown" {
		result["error"] = boundedProcedureDiagnostic(operation.Error)
	}
	return result
}

func boundedProcedureDiagnostic(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Procedure execution failed"
	}
	if len(value) > 2048 {
		value = value[:2048]
	}
	return value
}
