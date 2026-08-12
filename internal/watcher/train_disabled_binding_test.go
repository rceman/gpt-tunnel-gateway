package watcher

import "testing"

func TestTrainWatcherRejectsWrongRunBindingWhenSupervisionIsDisabled(t *testing.T) {
	train, start, runtime, run := trainWatcherFixture(t)
	run.ID = "GTW-TSK180-RUN1"
	if _, err := BindTrainRun(train, start, runtime, run); err == nil {
		t.Fatal("watcher accepted a different Run identity")
	}
}
