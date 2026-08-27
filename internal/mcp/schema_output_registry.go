package mcp

func mergeToolOutputSchemas(groups ...map[string]map[string]any) map[string]map[string]any {
	merged := map[string]map[string]any{}
	for _, group := range groups {
		for name, schema := range group {
			if _, exists := merged[name]; exists {
				panic("duplicate MCP output schema " + name)
			}
			merged[name] = schema
		}
	}
	return merged
}

var toolOutputSchemas = mergeToolOutputSchemas(
	coreToolOutputSchemas(),
	adrToolOutputSchemas(),
	runtimeToolOutputSchemas(),
)
