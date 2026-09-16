package workflowrole

type Role struct {
	Key            string
	Code           string
	ManagedRuntime bool
	RefRequired    bool
	RefSemantics   string
}

const (
	RolePlanner = "planner"
	RoleLead    = "lead"
	RoleAdvisor = "advisor"
	RoleWorker  = "worker"
)

var registry = [...]Role{
	{Key: RolePlanner, Code: "P"},
	{Key: RoleLead, Code: "L", ManagedRuntime: true, RefRequired: true, RefSemantics: "airelay_session_key"},
	{Key: RoleAdvisor, Code: "A", ManagedRuntime: true, RefRequired: true, RefSemantics: "airelay_session_key"},
	{Key: RoleWorker, Code: "W", ManagedRuntime: true, RefRequired: true, RefSemantics: "airelay_session_key"},
}

func Roles() []Role {
	roles := make([]Role, len(registry))
	copy(roles, registry[:])
	return roles
}

func Names() []string {
	roles := Roles()
	result := make([]string, 0, len(roles))
	for _, role := range roles {
		result = append(result, role.Key)
	}
	return result
}

func Schema(description string) map[string]any {
	enum := make([]any, 0, len(registry))
	for _, role := range registry {
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

func OutputSchema() map[string]any {
	return Schema("")
}

func ByKey(key string) (Role, bool) {
	for _, role := range registry {
		if role.Key == key {
			return role, true
		}
	}
	return Role{}, false
}

func ByCode(code string) (Role, bool) {
	for _, role := range registry {
		if role.Code == code {
			return role, true
		}
	}
	return Role{}, false
}

func Code(key string) (string, bool) {
	role, ok := ByKey(key)
	if !ok {
		return "", false
	}
	return role.Code, true
}

func Is(key string) bool {
	_, ok := ByKey(key)
	return ok
}

func RequiresRuntime(key string) bool {
	role, ok := ByKey(key)
	return ok && role.ManagedRuntime
}

func RequiresRef(key string) bool {
	role, ok := ByKey(key)
	return ok && role.RefRequired
}
