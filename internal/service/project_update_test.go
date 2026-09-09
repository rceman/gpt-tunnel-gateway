package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type projectUpdateFixture struct {
	s  *Service
	d  *sqlitestore.Databases
	c  config.Config
	id model.ProjectIdentifiers
}

func newProjectUpdateFixture(t *testing.T) projectUpdateFixture {
	t.Helper()
	s, revision, _ := testServiceWithoutIdentifiers(t)
	project := s.Config.Projects["example"]
	project.ProjectCode = "RSM"
	s.Config.Projects["example"] = project
	s.Config.Controller.TunnelHealthListenAddr = "127.0.0.1:8876"
	s.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	if err := fsutil.WriteJSONAtomic(s.ConfigPath, s.Config, 0o600); err != nil {
		t.Fatal(err)
	}
	id, _, err := s.ProjectIdentifiersAdopt(context.Background(), ProjectIdentifiersAdoptInput{
		ProjectID:   "example",
		ProjectCode: "RSM",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	s.Durability = d
	return projectUpdateFixture{
		s:  s,
		d:  d,
		c:  s.Config,
		id: id,
	}
}

func (f projectUpdateFixture) close() { _ = f.d.Close() }

func TestProjectUpdateBootstrapCorrectionPreservesAuthoritiesAndCounters(t *testing.T) {
	f := newProjectUpdateFixture(t)
	defer f.close()
	if _, err := f.s.ProjectUpdate(context.Background(), ProjectUpdateInput{
		ProjectID:   "example",
		ProjectCode: "MCP",
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := config.Load(f.s.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	got := updated.Projects["example"]
	want := f.c.Projects["example"]
	if got.ProjectCode != "MCP" || got.Root != want.Root || got.Mirror != want.Mirror || got.Remote != want.Remote || got.DefaultBranch != want.DefaultBranch || got.AirelaySessionKey != want.AirelaySessionKey {
		t.Fatalf("host project changed beyond code: got=%#v want=%#v", got, want)
	}
	ids, err := f.s.ProjectIdentifiersRead(context.Background(), "example")
	if err != nil || ids.ProjectCode != "MCP" || ids.NextTaskNumber != 1 || ids.NextADRNumber != 1 {
		t.Fatalf("unexpected Hub identifiers: %#v %v", ids, err)
	}
	rows, err := f.d.Shared.Query(context.Background(), `SELECT entity_type,project_code,next_number FROM shared_entity_sequences WHERE project_id='example' ORDER BY entity_type`)
	if err != nil || len(rows.Rows) != 2 {
		t.Fatalf("unexpected Shared sequences: %#v %v", rows.Rows, err)
	}
	for _, row := range rows.Rows {
		if row[1] != "MCP" || row[2] != int64(1) {
			t.Fatalf("unexpected Shared sequence: %#v", row)
		}
	}
	entity, err := f.d.ReadSharedEntity(context.Background(), "project_configuration", "example")
	if err != nil {
		t.Fatal(err)
	}
	var shared, hubConfig model.ProjectConfiguration
	if err := json.Unmarshal(entity.Payload, &shared); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Hub.ReadJSON(context.Background(), f.s.projectConfigurationPath("example"), &hubConfig); err != nil {
		t.Fatal(err)
	}
	if !sameProjectConfiguration(shared, hubConfig) {
		t.Fatalf("Shared configuration differs from Hub: shared=%#v hub=%#v", shared, hubConfig)
	}
}

func sameProjectConfiguration(a, b model.ProjectConfiguration) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func TestProjectUpdateRejectsNonVirginHubCountersWithoutMutation(t *testing.T) {
	f := newProjectUpdateFixture(t)
	defer f.close()
	before, err := f.s.ProjectIdentifiersRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	revision, err := f.s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Hub.Transact(context.Background(), revision, "test: nonvirgin counter", func(worktree string) ([]string, error) {
		path := f.s.projectIdentifiersPath("example")
		var ids model.ProjectIdentifiers
		if err := readWorktreeJSON(worktree, path, &ids); err != nil {
			return nil, err
		}
		ids.NextTaskNumber = 2
		if err := hubWriteJSONForTest(worktree, path, ids); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ProjectUpdate(context.Background(), ProjectUpdateInput{
		ProjectID:   "example",
		ProjectCode: "MCP",
	}); err == nil || !strings.Contains(err.Error(), "virgin") {
		t.Fatalf("non-virgin counters accepted: %v", err)
	}
	after, err := f.s.ProjectIdentifiersRead(context.Background(), "example")
	if err != nil || after != (model.ProjectIdentifiers{SchemaVersion: 1, ProjectID: "example", ProjectCode: "RSM", NextTaskNumber: 2, NextADRNumber: before.NextADRNumber}) {
		t.Fatalf("rejected update mutated identifiers: %#v %v", after, err)
	}
}

func TestProjectUpdateRejectsDurableStateAndDuplicateCode(t *testing.T) {
	for name, seed := range map[string]func(projectUpdateFixture){
		"task": func(f projectUpdateFixture) {
			_, _ = f.d.Shared.Exec(context.Background(), `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES('RSM-TSK1',1,?,?)`, []byte(`{"project_id":"example"}`), "now")
		},
		"session": func(f projectUpdateFixture) {
			_, _ = session.NewStoreWithDurability(f.d).Create(session.CreateInput{ProjectID: "example", ProjectCode: "RSM", Role: session.RolePlanner, SessionType: session.SessionTypeChatGPT})
		},
		"agent": func(f projectUpdateFixture) {
			_ = f.d.UpsertLocalAgent(context.Background(), sqlitestore.LocalAgent{ProjectID: "example", AgentID: "coding", Payload: []byte(`{"project_id":"example","agent_id":"coding"}`), UpdatedAt: "now"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newProjectUpdateFixture(t)
			defer f.close()
			seed(f)
			if _, err := f.s.ProjectUpdate(context.Background(), ProjectUpdateInput{
				ProjectID:   "example",
				ProjectCode: "MCP",
			}); err == nil {
				t.Fatalf("%s state accepted", name)
			}
		})
	}
}

func TestProjectUpdateSharedConflictCompensatesHubAndConfig(t *testing.T) {
	f := newProjectUpdateFixture(t)
	defer f.close()
	configuration, err := f.s.ProjectConfigurationRead(context.Background(), "example")
	if err == nil {
		t.Fatal("unexpected Shared configuration before bootstrap")
	}
	_ = configuration
	if err := f.d.PutSharedProjection(context.Background(), "project_configuration", sqlitestore.SharedEntity{ID: "example", Revision: 99, Payload: []byte(`{"project_id":"example","revision":99}`), UpdatedAt: "now"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ProjectUpdate(context.Background(), ProjectUpdateInput{
		ProjectID:   "example",
		ProjectCode: "MCP",
	}); err == nil {
		t.Fatal("conflicting Shared configuration accepted")
	}
	ids, err := f.s.ProjectIdentifiersRead(context.Background(), "example")
	if err != nil || ids.ProjectCode != "RSM" {
		t.Fatalf("Hub compensation failed: %#v %v", ids, err)
	}
	local, err := config.Load(f.s.ConfigPath)
	if err != nil || local.Projects["example"].ProjectCode != "RSM" {
		t.Fatalf("config compensation failed: %v", err)
	}
}

func TestProjectUpdateRejectsDuplicateTargetCode(t *testing.T) {
	f := newProjectUpdateFixture(t)
	defer f.close()
	revision, err := f.s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	secondRevision := registerIdentifierProject(t, f.s, "second", revision)
	if _, _, err := f.s.ProjectIdentifiersAdopt(context.Background(), ProjectIdentifiersAdoptInput{
		ProjectID:   "second",
		ProjectCode: "MCP",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: secondRevision,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ProjectUpdate(context.Background(), ProjectUpdateInput{
		ProjectID:   "example",
		ProjectCode: "MCP",
	}); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("duplicate target code accepted: %v", err)
	}
}

func hubWriteJSONForTest(worktree, path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(worktree, filepath.FromSlash(path)), append(data, '\n'), 0o600)
}
