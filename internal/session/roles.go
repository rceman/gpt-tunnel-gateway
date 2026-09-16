package session

import "github.com/rceman/gpt-tunnel-gateway/internal/workflowrole"

type WorkflowRole = workflowrole.Role

const (
	RolePlanner = workflowrole.RolePlanner
	RoleLead    = workflowrole.RoleLead
	RoleAdvisor = workflowrole.RoleAdvisor
	RoleWorker  = workflowrole.RoleWorker
)

func WorkflowRoles() []WorkflowRole {
	return workflowrole.Roles()
}

func WorkflowRoleNames() []string {
	return workflowrole.Names()
}

func WorkflowRoleSchema(description string) map[string]any {
	return workflowrole.Schema(description)
}

func WorkflowRoleOutputSchema() map[string]any {
	return workflowrole.OutputSchema()
}

func WorkflowRoleByKey(key string) (WorkflowRole, bool) {
	return workflowrole.ByKey(key)
}

func WorkflowRoleByCode(code string) (WorkflowRole, bool) {
	return workflowrole.ByCode(code)
}

func WorkflowRoleCode(key string) (string, bool) {
	return workflowrole.Code(key)
}

func IsWorkflowRole(key string) bool {
	return workflowrole.Is(key)
}

func WorkflowRoleRequiresRuntime(key string) bool {
	return workflowrole.RequiresRuntime(key)
}

func WorkflowRoleRequiresRef(key string) bool {
	return workflowrole.RequiresRef(key)
}
