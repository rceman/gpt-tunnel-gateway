package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
)

func TestTaskListLoadsRunIndexOnceForStartupStateCheck(t *testing.T) {
	s, hubRevision, _ := testService(t)
	for _, slug := range []string{"startup-one", "startup-two"} {
		created, result, err := s.TaskCreate(context.Background(), TaskCreateInput{
			ProjectID:          "example",
			Title:              "Startup task " + slug,
			Objective:          "Exercise startup state graph performance.",
			Slug:               slug,
			AcceptanceCriteria: []string{"bounded"},
			OperationClass:     "implementation",
			CreatedBy:          "test",
			WriteOptions: WriteOptions{
				ExpectedHubRevision: hubRevision,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		hubRevision = result.Hub.After
		if created.ID == "" {
			t.Fatal("task ID was empty")
		}
	}

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "git-args.log")
	wrapper := filepath.Join(filepath.Dir(logPath), "git")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + logPath + "'\nexec '" + gitPath + "' \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(wrapper)+string(os.PathListSeparator)+os.Getenv("PATH"))

	if _, err := s.TaskList(context.Background(), "example"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "ls-tree -r --name-only refs/remotes/origin/main -- gpt-tunnel/v1/projects/example/runs"
	if got := strings.Count(string(data), want); got != 1 {
		t.Fatalf("run index was loaded %d times, want once; git calls:\n%s", got, data)
	}
}

func TestReadOnlyBootstrapErrorIsBounded(t *testing.T) {
	state := t.TempDir()
	s := New(config.Config{StateDir: state})
	_, err := s.ProjectList(context.Background())
	if err == nil || err.Error() != "read-only hub lock unavailable" || strings.Contains(err.Error(), state) {
		t.Fatalf("unbounded read-only error: %v", err)
	}
}

func TestValidateConfiguredProjectRecordsRejectsMissingPlan(t *testing.T) {
	s, _, _ := testService(t)
	hubRevision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Hub.Transact(context.Background(), hubRevision, "test: remove initial plan", func(worktree string) ([]string, error) {
		path := filepath.Join(worktree, filepath.FromSlash(s.planPath("example")))
		if err := os.Remove(path); err != nil {
			return nil, err
		}
		return []string{s.planPath("example")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateConfiguredProjectRecords(context.Background()); err == nil || !strings.Contains(err.Error(), "plan") {
		t.Fatalf("missing durable plan was not rejected deterministically: %v", err)
	}
}

func TestHistoricalRunDoesNotBreakDispatchOrSessionSafety(t *testing.T) {
	s, hubRev, _ := testService(t)
	fixture, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "historical-run-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	reportFixture, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "historical-report-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Hub.Transact(context.Background(), hubRev, "test: historical terminal run", func(worktree string) ([]string, error) {
		path := s.runPath("example", "11111111-1111-4111-8111-111111111111")
		return []string{path}, hub.WriteText(worktree, path, string(fixture))
	})
	if err != nil {
		t.Fatal(err)
	}
	reportTx, err := s.Hub.Transact(context.Background(), tx.After, "test: historical report", func(worktree string) ([]string, error) {
		path := s.reportPath("example", "11111111-1111-4111-8111-111111111111")
		return []string{path}, hub.WriteText(worktree, path, string(reportFixture))
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunReport(context.Background(), "11111111-1111-4111-8111-111111111111"); err == nil || !strings.Contains(err.Error(), "history-only") {
		t.Fatalf("historical report was not isolated: %v", err)
	}
	runs, err := s.RunList(context.Background(), "example")
	if err != nil || len(runs) != 1 || !runs[0].Historical || runs[0].CompletionPath != "" {
		t.Fatalf("historical run list failed: %#v %v", runs, err)
	}
	task, created, err := s.TaskCreate(context.Background(), TaskCreateInput{
		ProjectID:          "example",
		Title:              "Dispatch after history",
		Objective:          "Dispatch after history.",
		Slug:               "history-safe",
		AcceptanceCriteria: []string{"works"},
		OperationClass:     "implementation",
		CreatedBy:          "gpt",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: reportTx.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = reportTx
	title, summary, objective := "History safe", "Dispatch after history", "Dispatch the new task after historical runs."
	plan, err := s.PlanUpdate(context.Background(), PlanUpdateInput{
		ProjectID:        "example",
		Title:            &title,
		Summary:          &summary,
		CurrentObjective: &objective,
		ActiveTaskID:     &task.ID,
		UpdatedBy:        "gpt",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TaskDispatch(context.Background(), DispatchInput{
		TaskID: task.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: plan.Hub.After,
		},
	}); err != nil {
		t.Fatalf("dispatch blocked by terminal historical run: %v", err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "gpt-tunnel", "v1", "projects", "example", "runs", "old", "run.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	activeFixture := strings.Replace(string(fixture), `"status": "succeeded"`, `"status": "awaiting_result"`, 1)
	if err := os.WriteFile(path, []byte(activeFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureSessionAvailableInWorktree(root, "example_master", 1<<20); err != nil {
		t.Fatalf("active historical run blocked a new operational session: %v", err)
	}
}
