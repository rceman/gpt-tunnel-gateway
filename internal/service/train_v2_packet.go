package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

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
	fmt.Fprintf(&b, "# Train v2 Task Packet\n\nTask: %s\nTrain: %s\nProject: %s\nRepository: %s\nExecution model: %s\nIntegration branch: %s\n\n## Objective\n\n%s\n\n## Acceptance criteria\n", task.ID, train.ID, project.ID, root, configuration.ExecutionModel, policy.IntegrationBranch, task.Objective)
	for _, criterion := range task.AcceptanceCriteria {
		fmt.Fprintf(&b, "- %s\n", criterion)
	}
	fmt.Fprintf(&b, "\n## Train item\n\nPosition: %d\nStatus: %s\nTask revision: %d\n", item.Position, item.Status, item.TaskRevision)
	if run != nil {
		fmt.Fprintf(&b, "Run: %s\nLane: %s\nBase: %s\n", run.ID, run.LaneBranch, run.BaseRevision)
	}
	b.WriteString("\n## Recovery\n\nThe Train and Run records are the execution authority. Plan history is not required for this operation.\n")
	return b.String()
}
