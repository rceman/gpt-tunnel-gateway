package model

import "testing"

func TestTaskScopeNormalizesAndBoundsFields(t *testing.T) {
	scope, err := NormalizeTaskScope(&TaskScope{Files: nil, Modules: []string{"gateway"}})
	if err != nil {
		t.Fatal(err)
	}
	if scope == nil || scope.Files == nil || scope.Modules[0] != "gateway" {
		t.Fatalf("scope was not normalized: %#v", scope)
	}
	if _, err := NormalizeTaskScope(&TaskScope{Files: []string{""}}); err == nil {
		t.Fatal("empty scope item was accepted")
	}
	if _, err := NormalizeTaskScope(&TaskScope{Modules: []string{"gateway\n"}}); err == nil {
		t.Fatal("control character in scope item was accepted")
	}
	tooMany := make([]string, MaxTaskScopeItems+1)
	for i := range tooMany {
		tooMany[i] = "module"
	}
	if _, err := NormalizeTaskScope(&TaskScope{Modules: tooMany}); err == nil {
		t.Fatal("overlarge scope was accepted")
	}
}

func TestTaskExecutionDefaultsAndRejectsUnknownValues(t *testing.T) {
	if got, err := NormalizeTaskExecution(""); err != nil || got != TaskExecutionTrain {
		t.Fatalf("default execution=%q err=%v", got, err)
	}
	if got := DefaultTaskExecution(""); got != TaskExecutionTrain {
		t.Fatalf("default execution=%q", got)
	}
	if _, err := NormalizeTaskExecution("other"); err == nil {
		t.Fatal("unknown execution was accepted")
	}
}
