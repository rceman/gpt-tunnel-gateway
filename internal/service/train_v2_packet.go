package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

const maxTrainV2PacketBytes = 256 << 10

// materializeTrainV2Packet derives the packet from the exact durable Task,
// Train item, Run, project configuration and local runtime. The packet lives
// beside the Gateway-owned completion destination, never in the Agent
// worktree, so it cannot make the checkout dirty.
func (s *Service) materializeTrainV2Packet(ctx context.Context, run model.Run, runtime trainv2.RuntimeBinding) (trainv2.AgentTaskPacket, error) {
	if run.TrainID == "" || runtime.RunID != run.ID || runtime.ProjectID != run.ProjectID || runtime.TrainID != run.TrainID {
		return trainv2.AgentTaskPacket{}, fmt.Errorf("Train packet identity does not match the Run/runtime")
	}
	if err := trainv2.ValidateRuntimeBinding(runtime, s.Config.StateDir); err != nil {
		return trainv2.AgentTaskPacket{}, err
	}
	task, err := s.TaskAuthoringRead(ctx, run.ProjectID, run.TaskID)
	if err != nil {
		return trainv2.AgentTaskPacket{}, err
	}
	train, err := s.TrainV2Read(ctx, run.ProjectID, run.TrainID)
	if err != nil {
		return trainv2.AgentTaskPacket{}, err
	}
	var item model.TrainV2Item
	found := false
	for _, candidate := range train.Items {
		if candidate.TaskID == run.TaskID && candidate.RunID == run.ID {
			item, found = candidate, true
			break
		}
	}
	if !found || item.Status != model.TrainV2ItemRunning || item.AgentID != run.AgentID {
		return trainv2.AgentTaskPacket{}, fmt.Errorf("Train packet is not bound to the active Run item")
	}
	if run.TaskSHA256 != task.RevisionSHA256 || item.TaskRevisionSHA256 != task.RevisionSHA256 {
		return trainv2.AgentTaskPacket{}, fmt.Errorf("Train packet Task revision does not match the Run")
	}
	project, err := s.ProjectRead(ctx, run.ProjectID)
	if err != nil {
		return trainv2.AgentTaskPacket{}, err
	}
	configuration, err := s.ProjectConfigurationRead(ctx, run.ProjectID)
	if err != nil {
		return trainv2.AgentTaskPacket{}, err
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, run.ProjectID)
	if err != nil {
		return trainv2.AgentTaskPacket{}, err
	}
	packetPath := filepath.Join(s.Config.StateDir, "runs", run.ID, "task-packet.md")
	if info, statErr := os.Lstat(packetPath); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return trainv2.AgentTaskPacket{}, fmt.Errorf("Train packet path is not a regular file")
		}
	} else if !os.IsNotExist(statErr) {
		return trainv2.AgentTaskPacket{}, statErr
	}
	text := "Packet path: " + packetPath + "\n\n" + renderTrainV2Packet(task, train, item, &run, project, configuration, policy, runtime.WorktreePath)
	if len([]byte(text)) > maxTrainV2PacketBytes {
		return trainv2.AgentTaskPacket{}, fmt.Errorf("Train packet exceeds %d bytes", maxTrainV2PacketBytes)
	}
	if err := fsutil.WriteFileAtomic(packetPath, []byte(text), 0o600); err != nil {
		return trainv2.AgentTaskPacket{}, err
	}
	return trainv2.AgentTaskPacket{Path: packetPath, WorktreePath: runtime.WorktreePath}, nil
}

// TrainV2TaskRead returns the Train-owned execution packet. It intentionally
// has no Plan field: Plan remains readable history, never required execution
// context after the project authority cutover.
func (s *Service) TrainV2TaskRead(ctx context.Context, projectID, taskID string) (TrainV2TaskPacket, error) {
	if err := requireTrainV2Authoring(ctx, s, projectID); err != nil {
		return TrainV2TaskPacket{}, err
	}
	task, err := s.TaskAuthoringRead(ctx, projectID, taskID)
	if err != nil {
		return TrainV2TaskPacket{}, err
	}
	trains, err := s.TrainV2List(ctx, TrainV2ListInput{ProjectID: projectID, Limit: model.MaxTrainV2Items})
	if err != nil && !IsNotFound(err) {
		return TrainV2TaskPacket{}, err
	}
	var found model.TrainV2
	var item model.TrainV2Item
	foundItem := false
	for _, candidate := range trains.Trains {
		for _, candidateItem := range candidate.Items {
			if candidateItem.TaskID == taskID {
				found, item, foundItem = candidate, candidateItem, true
				break
			}
		}
		if foundItem {
			break
		}
	}
	if !foundItem {
		project, err := s.ProjectRead(ctx, projectID)
		if err != nil {
			return TrainV2TaskPacket{}, err
		}
		configuration, err := s.ProjectConfigurationRead(ctx, projectID)
		if err != nil {
			return TrainV2TaskPacket{}, err
		}
		policy, err := s.ProjectWorkflowPolicyRead(ctx, projectID)
		if err != nil {
			return TrainV2TaskPacket{}, err
		}
		if _, err := s.projectConfig(projectID); err != nil {
			return TrainV2TaskPacket{}, err
		}
		return TrainV2TaskPacket{Task: task, ProjectConfiguration: configuration, WorkflowPolicy: policy, RepositoryRoot: "", Recovery: "This Task is planned and ready for Train admission; no execution runtime exists yet.", Text: renderTrainV2Packet(task, model.TrainV2{}, model.TrainV2Item{}, nil, project, configuration, policy, "")}, nil
	}
	var run *model.Run
	if item.RunID != "" {
		candidate, err := s.RunRead(ctx, item.RunID)
		if err != nil {
			return TrainV2TaskPacket{}, err
		}
		run = &candidate
	}
	project, err := s.ProjectRead(ctx, projectID)
	if err != nil {
		return TrainV2TaskPacket{}, err
	}
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return TrainV2TaskPacket{}, err
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, projectID)
	if err != nil {
		return TrainV2TaskPacket{}, err
	}
	if _, err := s.projectConfig(projectID); err != nil {
		return TrainV2TaskPacket{}, err
	}
	repositoryRoot := ""
	if run != nil && operationalActiveRun(*run) {
		runtime, runtimeErr := trainv2.ReadRuntime(s.Config.StateDir, projectID, found.ID)
		if runtimeErr != nil || runtime.RunID != run.ID {
			return TrainV2TaskPacket{}, fmt.Errorf("Train v2 Run has no exact local runtime binding")
		}
		repositoryRoot = runtime.WorktreePath
	}
	text := renderTrainV2Packet(task, found, item, run, project, configuration, policy, repositoryRoot)
	return TrainV2TaskPacket{Task: task, Train: found, Item: item, Run: run, ProjectConfiguration: configuration, WorkflowPolicy: policy, RepositoryRoot: repositoryRoot, Recovery: "Re-read this Train-owned packet and durable Train state before retrying after compaction.", Text: text}, nil
}

func renderTrainV2Packet(task model.TaskAuthoring, train model.TrainV2, item model.TrainV2Item, run *model.Run, project model.Project, configuration model.ProjectConfiguration, policy model.ProjectWorkflowPolicy, root string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Train v2 Task Packet\n\nTask: %s\nTask title: %s\nTask revision: %d\nTask revision SHA256: %s\nTask status: %s\nTask priority: %s\nCreated by: %s\nTrain: %s\nProject: %s\nRepository/worktree: %s\nExecution model: %s\nIntegration branch: %s\n", task.ID, task.Title, task.Revision, task.RevisionSHA256, task.Status, task.Priority, task.CreatedBy, train.ID, project.ID, root, configuration.ExecutionModel, policy.IntegrationBranch)
	if task.ReadySeal != nil {
		fmt.Fprintf(&b, "Ready seal: revision=%d sha256=%s ready_by=%s ready_at=%s\n", task.ReadySeal.Revision, task.ReadySeal.RevisionSHA256, task.ReadySeal.ReadyBy, task.ReadySeal.ReadyAt.UTC().Format(time.RFC3339Nano))
	}
	b.WriteString("\n## Objective\n\n")
	b.WriteString(task.Objective)
	b.WriteString("\n\n## Acceptance criteria\n")
	for _, criterion := range task.AcceptanceCriteria {
		fmt.Fprintf(&b, "- %s\n", criterion)
	}
	b.WriteString("\n## Constraints\n")
	for _, constraint := range task.Constraints {
		fmt.Fprintf(&b, "- %s\n", constraint)
	}
	b.WriteString("\n## Dependencies\n")
	for _, dependency := range task.Dependencies {
		fmt.Fprintf(&b, "- %s\n", dependency)
	}
	b.WriteString("\n## Preparation references\n")
	for _, reference := range task.PreparationReferences {
		fmt.Fprintf(&b, "- %s\n", reference)
	}
	fmt.Fprintf(&b, "\n## ADR context\n\nRelation: %s\n", task.ADRRelation)
	for _, reference := range task.ADRReferences {
		fmt.Fprintf(&b, "- %s\n", reference)
	}
	if len(task.Metadata) > 0 {
		b.WriteString("\n## Task metadata\n")
		keys := make([]string, 0, len(task.Metadata))
		for key := range task.Metadata {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&b, "- %s: %s\n", key, task.Metadata[key])
		}
	}
	fmt.Fprintf(&b, "\n## Train item\n\nPosition: %d\nStatus: %s\nTask revision: %d\n", item.Position, item.Status, item.TaskRevision)
	if run != nil {
		fmt.Fprintf(&b, "Run: %s\nLane: %s\nBase: %s\n", run.ID, run.LaneBranch, run.BaseRevision)
	}
	b.WriteString("\n## Recovery\n\nThe Train and Run records are the execution authority. Plan history is not required for this operation.\n")
	return b.String()
}
