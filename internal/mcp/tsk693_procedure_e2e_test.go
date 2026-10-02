package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func tsk693ScriptPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "procedures", "e2e.py"))
	if err != nil {
		t.Fatal(err)
	}
	if info, statErr := os.Stat(path); statErr != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("e2e procedure script is not an executable file: %s", path)
	}
	return path
}

func tsk693PrepareFixture(t *testing.T) *tsk571HTTPFixture {
	t.Helper()
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner, durableSession.RoleLead}, true, true)
	installTSK563Airelay(t, fixture)
	revision, err := fixture.server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	workerID, workerRuntime := "coding-tsk693-worker", "runtime-tsk693-worker"
	seedTSK571Agent(t, fixture.server.Service, revision, workerID, true)
	fixture.server.Service.Config.ProjectAgentBindings[fixture.projectID][workerID] = config.AgentBinding{SessionKey: workerRuntime}
	fixture.addSession(t, fixture.projectID, "EXM", durableSession.RoleWorker, workerRuntime)
	return fixture
}

// tsk693PendingTrack drives the fixture task to integrated and submits the
// Track so the target daemon carries a real review_pending snapshot.
func tsk693PendingTrack(t *testing.T, fixture *tsk571HTTPFixture) (string, map[string]any) {
	t.Helper()
	lead := fixture.sessions[durableSession.RoleLead]
	if result := fixture.call(t, lead, "task/dispatch", map[string]any{"key": fixture.task.ID}); result["ok"] != true {
		t.Fatalf("Lead could not dispatch fixture Task: %#v", result)
	}
	tsk593SetExecutionStatus(t, fixture, fixture.task.ID, model.TaskExecutionIntegrated)
	track := tsk593CreateTrack(t, fixture, []string{fixture.task.ID}, "e2e")
	if _, err := fixture.server.Service.TrackLifecycleSubmit(context.Background(), fixture.projectID, track.ID, lead); err != nil {
		t.Fatalf("Track submit failed: %v", err)
	}
	view := tsk593ActionResult(t, fixture.call(t, lead, "track/read", map[string]any{"key": track.ID}))
	if view["status"] != model.TrackReviewPending {
		t.Fatalf("seeded Track is not review_pending: %#v", view)
	}
	review, ok := view["review"].(map[string]any)
	if !ok {
		t.Fatalf("seeded Track has no review snapshot: %#v", view)
	}
	return track.ID, review
}

func tsk693RunScript(t *testing.T, script, dir string, input map[string]any) (int, string, map[string]any) {
	t.Helper()
	inputPath := filepath.Join(dir, "input.json")
	outputPath := filepath.Join(dir, "output.json")
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(context.Background(), "python3", script)
	command.Dir = dir
	command.Env = []string{
		"LC_ALL=C",
		"PATH=/usr/bin:/bin",
		"GTW_PROCEDURE_INPUT_FILE=" + inputPath,
		"GTW_PROCEDURE_OUTPUT_FILE=" + outputPath,
	}
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	code := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("e2e script could not run: %v", runErr)
		}
	}
	receipt := map[string]any(nil)
	if raw, readErr := os.ReadFile(outputPath); readErr == nil && len(raw) > 0 {
		if decodeErr := json.Unmarshal(raw, &receipt); decodeErr != nil {
			t.Fatalf("e2e script output is not valid JSON: %v", decodeErr)
		}
	}
	return code, stderr.String(), receipt
}

func tsk693ListenAddr(fixture *tsk571HTTPFixture) string {
	return strings.TrimPrefix(strings.TrimPrefix(fixture.http.URL, "http://"), "https://")
}

func tsk693Input(fixture *tsk571HTTPFixture, view map[string]any, planner, trackID string) map[string]any {
	review, _ := view["review"].(map[string]any)
	return map[string]any{
		"op":              "track_accept",
		"listen_addr":     tsk693ListenAddr(fixture),
		"target_project":  "EXM",
		"planner_session": planner,
		"target_track":    trackID,
		"track_revision":  review["track_revision"],
		"head":            review["head"],
		"tree":            review["tree"],
		"submitted_by":    review["submitted_by"],
		"submitted_at":    review["submitted_at"],
		"tasks":           review["tasks"],
	}
}

func TestTSK693E2ETrackAcceptCrossProject(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := tsk693PrepareFixture(t)
	planner := fixture.sessions[durableSession.RolePlanner]
	lead := fixture.sessions[durableSession.RoleLead]
	trackID, review := tsk693PendingTrack(t, fixture)
	script := tsk693ScriptPath(t)

	readView := func() map[string]any {
		return tsk593ActionResult(t, fixture.call(t, lead, "track/read", map[string]any{"key": trackID}))
	}

	// Happy path: exact snapshot accepted through the target Planner Session.
	code, stderr, receipt := tsk693RunScript(t, script, t.TempDir(), tsk693Input(fixture, readView(), planner, trackID))
	if code != 0 {
		t.Fatalf("e2e track_accept failed: rc=%d stderr=%s", code, stderr)
	}
	after := readView()
	if after["status"] != model.TrackAccepted {
		t.Fatalf("Track did not become accepted: %#v", after)
	}
	for _, field := range []string{"op", "target_project", "track", "revision", "head", "tree", "status"} {
		if _, present := receipt[field]; !present {
			t.Fatalf("receipt misses %q: %#v", field, receipt)
		}
	}
	if receipt["op"] != "track_accept" || receipt["target_project"] != "EXM" || receipt["track"] != trackID || receipt["status"] != "accepted" || receipt["head"] != review["head"] || receipt["tree"] != review["tree"] {
		t.Fatalf("receipt does not bind the accepted snapshot: %#v", receipt)
	}

	// Idempotent replay: an already-accepted Track with the same snapshot
	// returns the receipt without a second mutation.
	code, stderr, replay := tsk693RunScript(t, script, t.TempDir(), tsk693Input(fixture, readView(), planner, trackID))
	if code != 0 || replay["status"] != "accepted" || replay["track"] != trackID {
		t.Fatalf("e2e replay failed: rc=%d stderr=%s receipt=%#v", code, stderr, replay)
	}
	if again := readView(); again["status"] != model.TrackAccepted {
		t.Fatalf("replay disturbed the accepted Track: %#v", again)
	}
}

func TestTSK693E2ERejectsNonLoopbackAndDrift(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := tsk693PrepareFixture(t)
	planner := fixture.sessions[durableSession.RolePlanner]
	lead := fixture.sessions[durableSession.RoleLead]
	trackID, _ := tsk693PendingTrack(t, fixture)
	script := tsk693ScriptPath(t)

	readView := func() map[string]any {
		return tsk593ActionResult(t, fixture.call(t, lead, "track/read", map[string]any{"key": trackID}))
	}
	// Loopback is checked before any network interaction; unreachable hosts
	// prove nothing was sent.
	for _, addr := range []string{"192.0.2.1:8765", "example.com:443", "[2001:db8::1]:80"} {
		input := tsk693Input(fixture, readView(), planner, trackID)
		input["listen_addr"] = addr
		code, stderr, _ := tsk693RunScript(t, script, t.TempDir(), input)
		if code == 0 {
			t.Fatalf("non-loopback target %q was accepted", addr)
		}
		if !strings.Contains(stderr, "loopback") {
			t.Fatalf("non-loopback rejection %q lacks bounded reason: %s", addr, stderr)
		}
	}
	if view := readView(); view["status"] != model.TrackReviewPending {
		t.Fatalf("non-loopback attempts mutated the Track: %#v", view)
	}

	// Non-Planner session on the target daemon fails closed.
	worker := fixture.addSession(t, fixture.projectID, "EXM", durableSession.RoleWorker, "runtime-tsk693-worker")
	input := tsk693Input(fixture, readView(), planner, trackID)
	input["planner_session"] = worker
	if code, stderr, _ := tsk693RunScript(t, script, t.TempDir(), input); code == 0 || !strings.Contains(stderr, "planner") {
		t.Fatalf("non-Planner target Session was accepted: rc=%d stderr=%s", code, stderr)
	}

	// Snapshot drift on every pinned field fails closed without acceptance.
	drifts := map[string]func(map[string]any){
		"track_revision": func(in map[string]any) { in["track_revision"] = int(in["track_revision"].(float64)) + 1 },
		"head":           func(in map[string]any) { in["head"] = "00000000" },
		"tree":           func(in map[string]any) { in["tree"] = "ffffffff" },
		"submitted_by":   func(in map[string]any) { in["submitted_by"] = "HOM_EXM_P_forged" },
		"submitted_at":   func(in map[string]any) { in["submitted_at"] = "01-01-01T00:00:00" },
		"member_revision": func(in map[string]any) {
			tasks := in["tasks"].([]any)
			tasks[0].(map[string]any)["revision"] = 999
		},
		"target_project": func(in map[string]any) { in["target_project"] = "GTW" },
	}
	for name, mutate := range drifts {
		input := tsk693Input(fixture, readView(), planner, trackID)
		mutate(input)
		code, stderr, _ := tsk693RunScript(t, script, t.TempDir(), input)
		if code == 0 {
			t.Fatalf("drifted snapshot %s was accepted", name)
		}
		if strings.TrimSpace(stderr) == "" {
			t.Fatalf("drift rejection %s emitted no reason", name)
		}
		if view := readView(); view["status"] != model.TrackReviewPending {
			t.Fatalf("drift rejection %s mutated the Track: %#v", name, view)
		}
	}

	// A clean exact snapshot still succeeds after the rejection battery.
	code, stderr, _ := tsk693RunScript(t, script, t.TempDir(), tsk693Input(fixture, readView(), planner, trackID))
	if code != 0 {
		t.Fatalf("e2e track_accept failed after rejections: rc=%d stderr=%s", code, stderr)
	}
	if view := readView(); view["status"] != model.TrackAccepted {
		t.Fatalf("Track did not become accepted: %#v", view)
	}
}

func TestTSK693E2EProcedureActionRequiresPlanner(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := tsk693PrepareFixture(t)
	planner := fixture.sessions[durableSession.RolePlanner]
	lead := fixture.sessions[durableSession.RoleLead]

	definition, err := model.GTWE2EProcedureDefinition()
	if err != nil {
		t.Fatal(err)
	}
	// Point the example project root at a disposable checkout carrying the
	// real script so admission passes for an authorized caller.
	procedureRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(procedureRoot, "procedures"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(tsk693ScriptPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procedureRoot, "procedures", "e2e.py"), script, 0o700); err != nil {
		t.Fatal(err)
	}
	entry := fixture.server.Service.Config.Projects[fixture.projectID]
	entry.Root = procedureRoot
	fixture.server.Service.Config.Projects[fixture.projectID] = entry
	installCtx := service.WithAgentSessionID(authority.WithPlanner(context.Background()), planner)
	if _, err := fixture.server.Service.ConfigProcedureCreate(installCtx, service.ConfigProcedureCreateInput{
		ProjectID: fixture.projectID, Name: model.GTWE2EProcedureName, Definition: definition, Reason: "Install e2e for authority coverage",
	}); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{
		"op":              "track_accept",
		"listen_addr":     tsk693ListenAddr(fixture),
		"target_project":  "EXM",
		"planner_session": planner,
		"target_track":    "EXM-TRK1",
		"track_revision":  1,
		"head":            "12345678",
		"tree":            "87654321",
		"submitted_by":    lead,
		"submitted_at":    time.Now().UTC().Format("06-01-02T15:04:05"),
		"tasks":           []any{},
	}
	if result := fixture.call(t, lead, "procedure/e2e", input); result["ok"] != false {
		t.Fatalf("Lead must not invoke the Planner-approval e2e Procedure: %#v", result)
	}
	admission := fixture.call(t, planner, "procedure/e2e", input)
	if admission["ok"] != true {
		t.Fatalf("Planner invocation of e2e was rejected: %#v", admission)
	}
	result, _ := admission["result"].(map[string]any)
	if result == nil || result["op"] == "" || result["status"] != "accepted" {
		t.Fatalf("Planner e2e invocation produced no durable admission: %#v", admission)
	}
}
