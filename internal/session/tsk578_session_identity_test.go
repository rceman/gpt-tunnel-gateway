package session

import (
	"errors"
	"regexp"
	"testing"
)

func TestTSK578SessionIDsUseCanonicalGatewayProjectRoleAndSuffix(t *testing.T) {
	store, _ := testStore(t)
	pattern := regexp.MustCompile(`^HOM_EXM_[PLAW]_[a-z0-9]{5}$`)
	for _, role := range WorkflowRoles() {
		input := testCreateInput(role.Key)
		if role.RefRequired {
			ref := "runtime-tsk578"
			input.SessionRef = &ref
		}
		record, err := store.Create(input)
		if err != nil {
			t.Fatalf("create %s: %v", role.Key, err)
		}
		if !pattern.MatchString(record.ID) {
			t.Fatalf("%s ID=%q does not match canonical format", role.Key, record.ID)
		}
		if len(record.ID) < 5 || regexp.MustCompile(`[^a-z0-9]`).MatchString(record.ID[len(record.ID)-5:]) {
			t.Fatalf("%s suffix is not lowercase base36: %q", role.Key, record.ID[len(record.ID)-5:])
		}
	}
}

func TestTSK578GeneratedSuffixUsesDefaultServerGenerator(t *testing.T) {
	store, _ := testStore(t)
	if store.IDGenerator != nil {
		t.Fatal("test store unexpectedly replaced the server ID generator")
	}
	record, err := store.Create(testCreateInput(RolePlanner))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[a-z0-9]{5}$`).MatchString(record.ID[len(record.ID)-5:]) {
		t.Fatalf("server-generated suffix=%q", record.ID[len(record.ID)-5:])
	}
}

func TestTSK578RejectsInvalidIdentityInputsAndLegacyIDs(t *testing.T) {
	store, _ := testStore(t)
	cases := []struct {
		name  string
		store Store
		input CreateInput
	}{
		{name: "bad gateway", store: NewStoreWithGateway(store.Durability, "home_pc"), input: testCreateInput(RolePlanner)},
		{name: "bad project code", store: store, input: CreateInput{
			ProjectID:   "example",
			ProjectCode: "EX",
			Role:        RolePlanner,
			SessionType: SessionTypeChatGPT,
		}},
		{name: "bad role", store: store, input: CreateInput{
			ProjectID:   "example",
			ProjectCode: "EXM",
			Role:        "agent",
			SessionType: SessionTypeChatGPT,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.store.Create(tc.input); !errors.Is(err, ErrInvalidSession) {
				t.Fatalf("Create err=%v", err)
			}
		})
	}
	for _, legacyID := range []string{"SP-EXM-0123", "SA-EXM-0123", "S-EXM-0123"} {
		if _, err := store.Get(legacyID); !errors.Is(err, ErrInvalidSession) {
			t.Fatalf("Get(%q) err=%v", legacyID, err)
		}
	}
}

func TestTSK578ManagedRolesRequireRefAndPlannerDoesNot(t *testing.T) {
	store, _ := testStore(t)
	if _, err := store.Create(testCreateInput(RolePlanner)); err != nil {
		t.Fatalf("Planner without ref was rejected: %v", err)
	}
	for _, role := range []string{RoleLead, RoleAdvisor, RoleWorker} {
		input := testCreateInput(role)
		if _, err := store.Create(input); !errors.Is(err, ErrInvalidSession) {
			t.Fatalf("%s without ref err=%v", role, err)
		}
	}
	ref := "airelay-runtime-tsk578"
	for _, role := range []string{RoleLead, RoleAdvisor, RoleWorker} {
		input := testCreateInput(role)
		input.SessionRef = &ref
		record, err := store.Create(input)
		if err != nil {
			t.Fatalf("%s with ref: %v", role, err)
		}
		got, err := store.Get(record.ID)
		if err != nil || got.SessionRef == nil || *got.SessionRef != ref {
			t.Fatalf("%s ref binding=%#v err=%v", role, got, err)
		}
	}
}

func TestTSK578GatewayAndProjectIsolation(t *testing.T) {
	store, _ := testStore(t)
	otherGateway := NewStoreWithGateway(store.Durability, "XYZ")
	first, err := store.Create(testCreateInput(RolePlanner))
	if err != nil {
		t.Fatal(err)
	}
	otherInput := testCreateInput(RoleWorker)
	otherInput.ProjectID, otherInput.ProjectCode = "other", "OTH"
	ref := "runtime-tsk578"
	otherInput.SessionRef = &ref
	second, err := otherGateway.Create(otherInput)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || first.ID != "HOM_EXM_P_"+first.ID[len(first.ID)-5:] || second.ID != "XYZ_OTH_W_"+second.ID[len(second.ID)-5:] {
		t.Fatalf("identity isolation failed: first=%q second=%q", first.ID, second.ID)
	}
	if got, err := store.Get(second.ID); err != nil || got.ProjectID != "other" {
		t.Fatalf("cross-store lookup failed: got=%#v err=%v", got, err)
	}
}

func TestTSK578RoleRegistryOwnsCodesSchemasAndBindingPolicy(t *testing.T) {
	roles := WorkflowRoles()
	if len(roles) != 4 {
		t.Fatalf("workflow role count=%d", len(roles))
	}
	for _, key := range []string{RolePlanner, RoleLead, RoleAdvisor, RoleWorker} {
		if !IsWorkflowRole(key) {
			t.Fatalf("canonical registry role %q is not registered", key)
		}
	}
	seenCodes := map[string]struct{}{}
	for _, role := range roles {
		if role.Code == "" {
			t.Fatalf("registry role has no code=%#v", role)
		}
		if _, exists := seenCodes[role.Code]; exists {
			t.Fatalf("duplicate registry role code=%q", role.Code)
		}
		seenCodes[role.Code] = struct{}{}
		code, ok := WorkflowRoleCode(role.Key)
		if !ok || code != role.Code {
			t.Fatalf("code lookup for %q did not use registry", role.Key)
		}
		if role.Key == RolePlanner && role.RefRequired {
			t.Fatal("Planner unexpectedly requires a runtime reference")
		}
		if role.Key != RolePlanner && (!role.RefRequired || role.RefSemantics != "airelay_session_key") {
			t.Fatalf("managed role binding policy=%#v", role)
		}
	}
	if IsWorkflowRole("agent") || IsWorkflowRole("supervisor") {
		t.Fatal("compatibility role accepted")
	}
	schema := WorkflowRoleSchema("role")
	enum, ok := schema["enum"].([]any)
	if !ok || len(enum) != len(roles) {
		t.Fatalf("role schema enum=%#v", schema["enum"])
	}
	for _, value := range enum {
		if role, ok := value.(string); !ok || !IsWorkflowRole(role) {
			t.Fatalf("schema role %v is not registry-backed", value)
		}
	}
}
