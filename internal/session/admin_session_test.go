package session

import (
	"testing"
)

func TestAdminSessionIsDurableMachineScopedAndRevocable(t *testing.T) {
	store, _ := testStore(t)
	store.GatewayID = "HOM"
	store.AdminIDGenerator = func() (string, error) {
		return "HOM_ADM_0123456789abcdefghijklmnopqrstuv", nil
	}
	label := "remote onboarding"
	created, err := store.CreateAdmin(&label)
	if err != nil {
		t.Fatal(err)
	}
	if created.Role != RoleAdmin || created.ProjectID != "" || created.ProjectCode != "" || created.SessionType != SessionTypeAdmin {
		t.Fatalf("admin record=%#v", created)
	}
	loaded, err := store.Get(created.ID)
	if err != nil || loaded.ID != created.ID {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	ended, err := store.End(created.ID)
	if err != nil || ended.Status != StatusEnded {
		t.Fatalf("ended=%#v err=%v", ended, err)
	}
	if active, err := store.Get(created.ID); err != nil || active.Status != StatusEnded {
		t.Fatalf("revoked record=%#v err=%v", active, err)
	}
}

func TestAdminSessionCannotBeCreatedThroughWorkflowCreate(t *testing.T) {
	store, _ := testStore(t)
	if _, err := store.Create(CreateInput{
		ProjectID:   "example",
		ProjectCode: "EXM",
		Role:        RoleAdmin,
		SessionType: SessionTypeAdmin,
	}); err == nil {
		t.Fatal("workflow Create accepted an Admin Session")
	}
}
