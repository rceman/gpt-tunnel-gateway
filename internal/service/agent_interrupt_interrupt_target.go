package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

type interruptTarget struct{ SessionKey string }

func (s *Service) resolveAgentInterruptTarget(ctx context.Context, in AgentInterruptInput) (interruptTarget, bool, error) {
	train, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return interruptTarget{}, true, err
	}
	if train.ProjectID != in.ProjectID || in.ItemPosition < 0 || in.ItemPosition >= len(train.Items) {
		return interruptTarget{}, true, nil
	}
	item := train.Items[in.ItemPosition]
	if item.TaskID != in.TaskID || in.AttemptNumber == 0 || in.AttemptNumber > uint64(len(item.Attempts)) {
		return interruptTarget{}, true, nil
	}
	attempt := item.Attempts[in.AttemptNumber-1]
	if attempt.Number != in.AttemptNumber || attempt.AgentID != in.AgentID || attempt.Status != model.TrainV2AttemptRunning {
		return interruptTarget{}, true, nil
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, in.ProjectID, in.TrainID)
	if err != nil || runtime.ItemPosition != in.ItemPosition || runtime.TaskID != in.TaskID || runtime.AttemptNumber != in.AttemptNumber || runtime.AgentID != in.AgentID || runtime.SessionKey != attempt.AirelaySessionKey {
		return interruptTarget{}, true, nil
	}
	agent, err := s.AgentRead(ctx, in.ProjectID, in.AgentID)
	if err != nil {
		return interruptTarget{}, true, nil
	}
	agents, err := s.AgentList(ctx, in.ProjectID)
	if err != nil {
		return interruptTarget{}, true, nil
	}
	binding, ok := s.resolveLocalAgentBinding(in.ProjectID, agent, agents)
	if !ok || binding.SessionKey != attempt.AirelaySessionKey {
		return interruptTarget{}, true, nil
	}
	return interruptTarget{SessionKey: binding.SessionKey}, false, nil
}
func validateAgentInterruptInput(in AgentInterruptInput) error {
	if model.ValidateObjectIdentifier(in.OperationID) != nil || model.ValidateProjectIdentifier(in.ProjectID) != nil || model.ValidateObjectIdentifier(in.TrainID) != nil || model.ValidateCanonicalTaskID(in.TaskID) != nil || model.ValidateObjectIdentifier(in.AgentID) != nil || in.ItemPosition < 0 || in.AttemptNumber < 1 {
		return fmt.Errorf("invalid agent interrupt execution identity")
	}
	if len(in.Message) > 256 || strings.ContainsRune(in.Message, 0) {
		return fmt.Errorf("invalid replacement prompt")
	}
	return nil
}
func agentInterruptRequestDigest(in AgentInterruptInput) (string, error) {
	data, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
func agentInterruptLockKey(in AgentInterruptInput) string {
	digest, _ := agentInterruptRequestDigest(in)
	return digest[:16]
}
func (s *Service) agentInterruptReceiptPath(projectID, operationID string) string {
	digest := sha256.Sum256([]byte(operationID))
	return filepath.Join(s.Config.StateDir, "agent-interrupts", projectID, hex.EncodeToString(digest[:])+".json")
}
func readAgentInterruptReceipt(path, requestDigest string) (agentInterruptReceipt, bool, error) {
	var receipt agentInterruptReceipt
	if err := fsutil.ReadJSONBounded(path, agentInterruptReceiptLimit, &receipt); err != nil {
		if os.IsNotExist(err) {
			return agentInterruptReceipt{}, false, nil
		}
		return agentInterruptReceipt{}, false, err
	}
	if receipt.RequestSHA256 != requestDigest {
		return agentInterruptReceipt{}, false, fmt.Errorf("agent interrupt operation_id is bound to different execution identity")
	}
	return receipt, true, nil
}
func (s *Service) persistAgentInterruptReceipt(path, requestDigest string, receipt agentInterruptReceipt) error {
	receipt.RequestSHA256 = requestDigest
	return fsutil.WriteJSONAtomic(path, receipt, 0o600)
}
