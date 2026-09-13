package session

type WorkflowRole struct {
	Key            string
	Code           string
	ManagedRuntime bool
	RefRequired    bool
	RefSemantics   string
}

var workflowRoleRegistry = [...]WorkflowRole{
	{Key: RolePlanner, Code: "P"},
	{Key: RoleLead, Code: "L", ManagedRuntime: true, RefRequired: true, RefSemantics: "airelay_session_key"},
	{Key: RoleAdvisor, Code: "A", ManagedRuntime: true, RefRequired: true, RefSemantics: "airelay_session_key"},
	{Key: RoleWorker, Code: "W", ManagedRuntime: true, RefRequired: true, RefSemantics: "airelay_session_key"},
}

func WorkflowRoles() []WorkflowRole {
	roles := make([]WorkflowRole, len(workflowRoleRegistry))
	copy(roles, workflowRoleRegistry[:])
	return roles
}

func WorkflowRoleNames() []string {
	roles := WorkflowRoles()
	result := make([]string, 0, len(roles))
	for _, role := range roles {
		result = append(result, role.Key)
	}
	return result
}

func WorkflowRoleSchema(description string) map[string]any {
	enum := make([]any, 0, len(workflowRoleRegistry))
	for _, role := range workflowRoleRegistry {
		enum = append(enum, role.Key)
	}
	return map[string]any{
		"type":        "string",
		"description": description,
		"enum":        enum,
		"minLength":   1,
		"maxLength":   16,
	}
}

func WorkflowRoleOutputSchema() map[string]any {
	return WorkflowRoleSchema("")
}

func WorkflowRoleByKey(key string) (WorkflowRole, bool) {
	for _, role := range workflowRoleRegistry {
		if role.Key == key {
			return role, true
		}
	}
	return WorkflowRole{}, false
}

func WorkflowRoleByCode(code string) (WorkflowRole, bool) {
	for _, role := range workflowRoleRegistry {
		if role.Code == code {
			return role, true
		}
	}
	return WorkflowRole{}, false
}

func WorkflowRoleCode(key string) (string, bool) {
	role, ok := WorkflowRoleByKey(key)
	if !ok {
		return "", false
	}
	return role.Code, true
}

func IsWorkflowRole(key string) bool {
	_, ok := WorkflowRoleByKey(key)
	return ok
}

func WorkflowRoleRequiresRuntime(key string) bool {
	role, ok := WorkflowRoleByKey(key)
	return ok && role.ManagedRuntime
}

func WorkflowRoleRequiresRef(key string) bool {
	role, ok := WorkflowRoleByKey(key)
	return ok && role.RefRequired
}
