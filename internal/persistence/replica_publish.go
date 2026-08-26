package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

var errPublishAlreadyApplied = errors.New("shared publish already applied")

func (r hubReplica) PublishShared(ctx context.Context, intent PublishIntent) error {
	if intent.EntityID == "" || len(intent.Payload) == 0 {
		return fmt.Errorf("shared publish intent is incomplete")
	}
	switch intent.Kind {
	case PublishTask:
		var task model.TaskAuthoring
		if err := decodeStrict(intent.Payload, &task); err != nil {
			return err
		}
		if err := model.ValidateTaskAuthoring(task); err != nil {
			return err
		}
		if task.ID != intent.EntityID {
			return fmt.Errorf("shared task publish identity mismatch")
		}
		path, err := taskPath(task.ProjectID, task.ID)
		if err != nil {
			return err
		}
		return r.publishJSON(ctx, path, "gateway: publish Shared task "+task.ID, task, func(raw []byte) (bool, error) {
			var latest model.TaskAuthoring
			if err := decodeStrict(raw, &latest); err != nil {
				return false, err
			}
			if latest.Revision > task.Revision {
				return false, fmt.Errorf("Hub task changed while publishing Shared outbox")
			}
			equal, err := canonicalJSONEqual(raw, task)
			return latest.Revision == task.Revision && equal, err
		})
	case PublishADR:
		var adr model.ADR
		if err := decodeStrict(intent.Payload, &adr); err != nil {
			return err
		}
		if err := model.ValidateADR(adr); err != nil {
			return err
		}
		if adr.ID != intent.EntityID {
			return fmt.Errorf("shared ADR publish identity mismatch")
		}
		path, err := adrPath(adr.ProjectID, adr.ID)
		if err != nil {
			return err
		}
		return r.publishJSON(ctx, path, "gateway: publish Shared ADR "+adr.ID, adr, func(raw []byte) (bool, error) {
			return canonicalJSONEqual(raw, adr)
		})
	case PublishProjectConfiguration:
		var configuration model.ProjectConfiguration
		if err := decodeStrict(intent.Payload, &configuration); err != nil {
			return err
		}
		normalizeProjectConfiguration(&configuration)
		if err := model.ValidateProjectConfiguration(configuration); err != nil {
			return err
		}
		if configuration.ProjectID != intent.EntityID {
			return fmt.Errorf("shared project configuration identity mismatch")
		}
		path, err := projectConfigurationPath(configuration.ProjectID)
		if err != nil {
			return err
		}
		return r.publishJSON(ctx, path, "gateway: publish Shared project configuration "+configuration.ProjectID, configuration, func(raw []byte) (bool, error) {
			var latest model.ProjectConfiguration
			if err := decodeStrict(raw, &latest); err != nil {
				return false, err
			}
			normalizeProjectConfiguration(&latest)
			if latest.Revision > configuration.Revision {
				return false, fmt.Errorf("Hub project configuration is newer than Shared outbox")
			}
			if latest.Revision == configuration.Revision {
				equal, err := canonicalValuesEqual(latest, configuration)
				if err != nil {
					return false, err
				}
				if equal {
					return true, nil
				}
				return false, fmt.Errorf("Hub project configuration conflicts with Shared outbox")
			}
			return false, nil
		})
	case PublishAgent:
		var agent model.Agent
		if err := decodeStrict(intent.Payload, &agent); err != nil {
			return err
		}
		if err := model.ValidateAgent(agent); err != nil {
			return err
		}
		if sharedAgentID(agent.ProjectID, agent.AgentID) != intent.EntityID {
			return fmt.Errorf("shared Agent publish identity mismatch")
		}
		path, err := agentPath(agent.ProjectID, agent.AgentID)
		if err != nil {
			return err
		}
		return r.publishJSON(ctx, path, "gateway: publish Shared Agent "+agent.ProjectID+"/"+agent.AgentID, agent, func(raw []byte) (bool, error) {
			return canonicalJSONEqual(raw, agent)
		})
	case PublishTrain:
		var train model.TrainV2
		if err := decodeStrict(intent.Payload, &train); err != nil {
			return err
		}
		if err := model.ValidateTrainV2(train); err != nil {
			return err
		}
		if train.ID != intent.EntityID {
			return fmt.Errorf("shared Train publish identity mismatch")
		}
		path, err := trainPath(train.ProjectID, train.ID)
		if err != nil {
			return err
		}
		return r.publishJSON(ctx, path, "gateway: publish Shared Train "+train.ID, train, func(raw []byte) (bool, error) {
			var latest model.TrainV2
			if err := decodeStrict(raw, &latest); err != nil {
				return false, err
			}
			if latest.Revision > train.Revision {
				return false, fmt.Errorf("Hub Train changed while publishing Shared outbox")
			}
			equal, err := canonicalJSONEqual(raw, train)
			return latest.Revision == train.Revision && equal, err
		})
	case PublishIntegrationReceipt:
		var receipt trainv2.IntegrationReceipt
		if err := decodeStrict(intent.Payload, &receipt); err != nil {
			return err
		}
		if err := trainv2.ValidateIntegrationReceipt(receipt); err != nil {
			return err
		}
		if sharedIntegrationID(receipt.ProjectID, receipt.TrainID) != intent.EntityID {
			return fmt.Errorf("shared integration receipt identity mismatch")
		}
		path, err := integrationReceiptPath(receipt.ProjectID, receipt.TrainID)
		if err != nil {
			return err
		}
		return r.publishJSON(ctx, path, "gateway: publish Shared Train integration receipt "+receipt.TrainID, receipt, func(raw []byte) (bool, error) {
			var latest trainv2.IntegrationReceipt
			if err := decodeStrict(raw, &latest); err != nil {
				return false, err
			}
			if latest.UpdatedAt.After(receipt.UpdatedAt) {
				return false, fmt.Errorf("Hub integration receipt changed while publishing Shared outbox")
			}
			equal, err := canonicalJSONEqual(raw, receipt)
			return latest.UpdatedAt.Equal(receipt.UpdatedAt) && equal, err
		})
	case PublishIntegrationOperation:
		var operation trainv2.IntegrationOperation
		if err := decodeStrict(intent.Payload, &operation); err != nil {
			return err
		}
		if err := trainv2.ValidateIntegrationOperation(operation); err != nil {
			return err
		}
		if sharedIntegrationID(operation.ProjectID, operation.TrainID) != intent.EntityID {
			return fmt.Errorf("shared integration operation identity mismatch")
		}
		path, err := integrationOperationPath(operation.ProjectID, operation.TrainID)
		if err != nil {
			return err
		}
		return r.publishJSON(ctx, path, "gateway: publish Shared Train integration operation "+operation.OperationID, operation, func(raw []byte) (bool, error) {
			var latest trainv2.IntegrationOperation
			if err := decodeStrict(raw, &latest); err != nil {
				return false, err
			}
			if latest.UpdatedAt.After(operation.UpdatedAt) {
				return false, fmt.Errorf("Hub integration operation changed while publishing Shared outbox")
			}
			equal, err := canonicalJSONEqual(raw, operation)
			return latest.UpdatedAt.Equal(operation.UpdatedAt) && equal, err
		})
	case PublishJournal:
		var event model.OperatorJournalEvent
		if err := decodeStrict(intent.Payload, &event); err != nil {
			return err
		}
		if err := model.ValidateOperatorJournalEvent(event); err != nil {
			return err
		}
		if event.ID != intent.EntityID {
			return fmt.Errorf("shared operator journal identity mismatch")
		}
		return r.publishJournal(ctx, event, intent.Payload)
	case PublishWatcherGuide:
		var guide model.WatcherGuide
		if err := decodeStrict(intent.Payload, &guide); err != nil {
			return err
		}
		if err := model.ValidateWatcherGuide(guide); err != nil {
			return err
		}
		if guide.ProjectID != intent.EntityID {
			return fmt.Errorf("shared watcher guide identity mismatch")
		}
		path, err := watcherGuidePath(guide.ProjectID)
		if err != nil {
			return err
		}
		return r.publishJSON(ctx, path, "gateway: publish Shared watcher guide "+guide.ProjectID, guide, func(raw []byte) (bool, error) {
			var latest model.WatcherGuide
			if err := decodeStrict(raw, &latest); err != nil {
				return false, err
			}
			if latest.Revision > guide.Revision {
				return false, fmt.Errorf("Hub watcher guide changed while publishing Shared outbox")
			}
			equal, err := canonicalJSONEqual(raw, guide)
			return latest.Revision == guide.Revision && equal, err
		})
	case PublishAttemptReport:
		var report model.TrainV2AttemptReport
		if err := decodeStrict(intent.Payload, &report); err != nil {
			return err
		}
		if err := model.ValidateTrainV2AttemptReport(report); err != nil {
			return err
		}
		if intent.ProjectID != "" && intent.ProjectID != report.ProjectID {
			return fmt.Errorf("Attempt report publish project mismatch")
		}
		path := hub.TrainAttemptReportPath(report.ProjectID, report.TrainID, report.ItemPosition, report.AttemptNumber)
		if intent.EntityID != path {
			return fmt.Errorf("Attempt report publish identity mismatch")
		}
		return r.publishJSON(ctx, path, "gateway: publish Attempt report "+path, report, func(raw []byte) (bool, error) {
			var latest model.TrainV2AttemptReport
			if err := decodeStrict(raw, &latest); err != nil {
				return false, err
			}
			return canonicalJSONEqual(raw, report)
		})
	case PublishAttemptReview:
		var review model.TrainV2AttemptReview
		if err := decodeStrict(intent.Payload, &review); err != nil {
			return err
		}
		if err := model.ValidateTrainV2AttemptReview(review); err != nil {
			return err
		}
		if intent.EntityID != review.ID {
			return fmt.Errorf("Attempt review publish identity mismatch")
		}
		if intent.ProjectID == "" {
			return fmt.Errorf("Attempt review publish project is required")
		}
		path := hub.TrainAttemptReviewPath(intent.ProjectID, review.TrainID, review.ItemPosition, review.AttemptNumber)
		return r.publishJSON(ctx, path, "gateway: publish Attempt review "+path, review, func(raw []byte) (bool, error) {
			var latest model.TrainV2AttemptReview
			if err := decodeStrict(raw, &latest); err != nil {
				return false, err
			}
			return canonicalJSONEqual(raw, review)
		})
	default:
		return fmt.Errorf("unsupported shared publish kind %q", intent.Kind)
	}
}

func (r hubReplica) publishJSON(ctx context.Context, path, subject string, value any, same func([]byte) (bool, error)) error {
	_, err := r.store.Transact(ctx, "", subject, func(worktree string) ([]string, error) {
		raw, readErr := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(path)))
		if readErr == nil {
			equal, err := same(raw)
			if err != nil {
				return nil, err
			}
			if equal {
				return nil, errPublishAlreadyApplied
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return nil, readErr
		}
		if err := hub.WriteJSON(worktree, path, value); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if errors.Is(err, errPublishAlreadyApplied) {
		return nil
	}
	return err
}

func (r hubReplica) publishJournal(ctx context.Context, event model.OperatorJournalEvent, payload []byte) error {
	eventPath, err := operatorEventPath(event.ProjectID, event.ID)
	if err != nil {
		return err
	}
	_, number, err := model.ParseAnyJournalEventID(event.ID)
	if err != nil {
		return err
	}
	counterPath, err := operatorCounterPath(event.ProjectID)
	if err != nil {
		return err
	}
	_, err = r.store.Transact(ctx, "", "gateway: publish Shared operator journal "+event.ID, func(worktree string) ([]string, error) {
		raw, readErr := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(eventPath)))
		if readErr == nil {
			if equal, err := canonicalJSONEqual(raw, event); err != nil {
				return nil, err
			} else if equal {
				return nil, errPublishAlreadyApplied
			}
			return nil, fmt.Errorf("Hub operator event changed while publishing Shared outbox")
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return nil, readErr
		}
		counter := model.OperatorJournalCounter{SchemaVersion: model.OperatorJournalSchemaVersion, ProjectID: event.ProjectID, NextEventNumber: number + 1}
		counterRaw, counterErr := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(counterPath)))
		if counterErr == nil {
			var existing model.OperatorJournalCounter
			if err := decodeStrict(counterRaw, &existing); err != nil {
				return nil, err
			}
			if existing.NextEventNumber > counter.NextEventNumber {
				counter = existing
			}
		} else if !errors.Is(counterErr, os.ErrNotExist) {
			return nil, counterErr
		}
		if err := hub.WriteJSON(worktree, eventPath, event); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, counterPath, counter); err != nil {
			return nil, err
		}
		return []string{eventPath, counterPath}, nil
	})
	if errors.Is(err, errPublishAlreadyApplied) {
		return nil
	}
	return err
}

func decodeStrict(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}

func canonicalJSONEqual[T any](raw []byte, expected T) (bool, error) {
	var actual T
	if err := decodeStrict(raw, &actual); err != nil {
		return false, err
	}
	return canonicalValuesEqual(actual, expected)
}

func canonicalValuesEqual(actual, expected any) (bool, error) {
	actualJSON, err := json.Marshal(actual)
	if err != nil {
		return false, err
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		return false, err
	}
	return bytes.Equal(actualJSON, expectedJSON), nil
}

func normalizeProjectConfiguration(configuration *model.ProjectConfiguration) {
	if configuration.Workflow.GateCommands.IsZero() {
		configuration.Workflow.GateCommands = model.DefaultProjectGateCommands()
	}
	if configuration.Integration.TargetBranch == "" {
		configuration.Integration.TargetBranch = configuration.Workflow.IntegrationBranch
	}
}

func projectPrefix(projectID string) (string, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join(hub.ProtocolRoot, "projects", projectID)), nil
}

func projectConfigurationPath(projectID string) (string, error) {
	prefix, err := projectPrefix(projectID)
	if err != nil {
		return "", err
	}
	return prefix + "/configuration/current.json", nil
}

func taskPath(projectID, taskID string) (string, error) {
	prefix, err := projectPrefix(projectID)
	if err != nil {
		return "", err
	}
	if err := model.ValidateCanonicalTaskID(taskID); err != nil {
		return "", err
	}
	return prefix + "/tasks-v2/" + taskID + ".json", nil
}

func adrPath(projectID, adrID string) (string, error) {
	prefix, err := projectPrefix(projectID)
	if err != nil {
		return "", err
	}
	if model.ValidateADRIdentifier(adrID) != nil && model.ValidateCanonicalADRIdentifier(adrID) != nil {
		return "", fmt.Errorf("invalid ADR identifier")
	}
	return prefix + "/adrs/" + adrID + ".json", nil
}

func agentPath(projectID, agentID string) (string, error) {
	prefix, err := projectPrefix(projectID)
	if err != nil {
		return "", err
	}
	if err := model.ValidateObjectIdentifier(agentID); err != nil {
		return "", err
	}
	return prefix + "/agents/" + agentID + ".json", nil
}

func trainPath(projectID, trainID string) (string, error) {
	prefix, err := projectPrefix(projectID)
	if err != nil {
		return "", err
	}
	if _, _, err := model.ParseTrainV2ID(trainID); err != nil {
		return "", err
	}
	return prefix + "/trains-v2/" + trainID + ".json", nil
}

func integrationReceiptPath(projectID, trainID string) (string, error) {
	path, err := trainPath(projectID, trainID)
	if err != nil {
		return "", err
	}
	return path[:len(path)-len(".json")] + ".integration.json", nil
}

func integrationOperationPath(projectID, trainID string) (string, error) {
	path, err := trainPath(projectID, trainID)
	if err != nil {
		return "", err
	}
	return path[:len(path)-len(".json")] + ".integration-operation.json", nil
}

func operatorEventPath(projectID, eventID string) (string, error) {
	prefix, err := projectPrefix(projectID)
	if err != nil {
		return "", err
	}
	if err := model.ValidateAnyOperatorEventID(eventID); err != nil {
		return "", err
	}
	return prefix + "/operator-journal/events/" + eventID + ".json", nil
}

func operatorCounterPath(projectID string) (string, error) {
	prefix, err := projectPrefix(projectID)
	if err != nil {
		return "", err
	}
	return prefix + "/operator-journal/counter.json", nil
}

func watcherGuidePath(projectID string) (string, error) {
	prefix, err := projectPrefix(projectID)
	if err != nil {
		return "", err
	}
	return prefix + "/watcher/guide.json", nil
}

func sharedAgentID(projectID, agentID string) string { return projectID + "\x00" + agentID }

func sharedIntegrationID(projectID, trainID string) string { return projectID + "\x00" + trainID }
