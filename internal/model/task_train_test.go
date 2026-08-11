package model

import (
	"strings"
	"testing"
	"time"
)

func validTaskTrainForTest() TaskTrain {
	return TaskTrain{
		SchemaVersion: TaskTrainSchemaVersion,
		ID:            "current",
		ProjectID:     "example",
		TaskIDs:       []string{"EXM-TSK1", "EXM-TSK2"},
		CurrentIndex:  0,
		CurrentTaskID: "EXM-TSK1",
		Status:        TaskTrainActive,
		UpdatedAt:     time.Now().UTC(),
	}
}

func TestValidateTaskTrainRequiresExplicitOrderedUniqueTasks(t *testing.T) {
	v := validTaskTrainForTest()
	if err := ValidateTaskTrain(v); err != nil {
		t.Fatal(err)
	}
	v.TaskIDs = []string{"EXM-TSK1", "EXM-TSK1"}
	if err := ValidateTaskTrain(v); err == nil {
		t.Fatal("duplicate task train IDs were accepted")
	}
	v = validTaskTrainForTest()
	v.Status = TaskTrainWaitingDelivery
	v.WaitReason = "delivery_review_or_merge_required"
	if err := ValidateTaskTrain(v); err != nil {
		t.Fatalf("delivery wait state rejected: %v", err)
	}
	v.Status = TaskTrainBlocked
	v.WaitReason = "current_task_cancelled"
	if err := ValidateTaskTrain(v); err != nil {
		t.Fatalf("blocked train rejected: %v", err)
	}
	v.WaitReason = strings.Repeat(" ", 2)
	if err := ValidateTaskTrain(v); err == nil {
		t.Fatal("blank blocked reason was accepted")
	}
}

func TestValidateTaskTrainCompletedIndexMustConsumeExplicitList(t *testing.T) {
	v := validTaskTrainForTest()
	v.Status = TaskTrainCompleted
	v.CurrentIndex = len(v.TaskIDs)
	if err := ValidateTaskTrain(v); err != nil {
		t.Fatal(err)
	}
	v.CurrentIndex--
	if err := ValidateTaskTrain(v); err == nil {
		t.Fatal("completed train with pending task was accepted")
	}
}

func TestTaskTrainExecutionGroupsAreOrderedAndPartitioned(t *testing.T) {
	v := validTaskTrainForTest()
	v.TrainID = "train-ordered"
	v.ID = ""
	v.ExecutionGroups = []ExecutionGroup{
		{GroupID: "group-1", TaskIDs: []string{"EXM-TSK1"}, RecommendedReasoning: TaskTrainReasoningSingleton},
		{GroupID: "group-2", TaskIDs: []string{"EXM-TSK2"}, RecommendedReasoning: TaskTrainReasoningGroup},
	}
	if err := ValidateTaskTrain(v); err != nil {
		t.Fatal(err)
	}
	v.ExecutionGroups[1].TaskIDs = []string{"EXM-TSK1"}
	if err := ValidateTaskTrain(v); err == nil {
		t.Fatal("overlapping execution groups were accepted")
	}
	v = validTaskTrainForTest()
	v.TrainID = "train-ordered"
	v.ID = ""
	v.ExecutionGroups = []ExecutionGroup{
		{GroupID: "group-1", TaskIDs: []string{"EXM-TSK2"}, RecommendedReasoning: TaskTrainReasoningSingleton},
		{GroupID: "group-2", TaskIDs: []string{"EXM-TSK1"}, RecommendedReasoning: TaskTrainReasoningSingleton},
	}
	if err := ValidateTaskTrain(v); err == nil {
		t.Fatal("out-of-order execution groups were accepted")
	}
}

func TestTaskTrainDefaultExecutionGroupsUseSingletonAndGroupReasoning(t *testing.T) {
	singleton := DefaultExecutionGroups([]string{"EXM-TSK1"}, "")
	if len(singleton) != 1 || singleton[0].RecommendedReasoning != TaskTrainReasoningSingleton {
		t.Fatalf("unexpected singleton defaults: %#v", singleton)
	}
	group := DefaultExecutionGroups([]string{"EXM-TSK1", "EXM-TSK2"}, "")
	if len(group) != 1 || group[0].RecommendedReasoning != TaskTrainReasoningGroup {
		t.Fatalf("unexpected group defaults: %#v", group)
	}
}
