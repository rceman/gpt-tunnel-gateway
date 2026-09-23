package actioncontract

import (
	"fmt"
	"strings"
)

func validateRequiredDefinitionNames(definitions map[string]definitionSpec) error {
	for _, name := range requiredDefinitions {
		if _, ok := definitions[name]; !ok {
			return fmt.Errorf("shared definitions: required definition %q is missing", name)
		}
	}
	return nil
}

func requiredSharedDefinitionNames() []string {
	return append([]string(nil), requiredDefinitions...)
}

func isKnownCrossDomainField(fields []string, name string) bool {
	return containsString(fields, name)
}

func canonicalPath(path string) bool {
	domain, _, ok := splitActionPath(path)
	return ok && !isRetiredDomain(domain) && !isRetiredAction(path)
}

func validateMetadataText(value string) bool {
	return strings.TrimSpace(value) != ""
}
