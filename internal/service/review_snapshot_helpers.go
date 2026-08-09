package service

func nonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func snapshotStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string{}, values...)
}
