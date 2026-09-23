package publicprojection

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const gitFingerprintCapacity = 65536

var gitFingerprints = struct {
	sync.RWMutex
	values    map[string]map[string]string
	ambiguous map[string]map[string]struct{}
	counts    map[string]int
}{
	values:    make(map[string]map[string]string),
	ambiguous: make(map[string]map[string]struct{}),
	counts:    make(map[string]int),
}

func Project(value any) (any, error) {
	return ProjectForScope(value, "")
}

func ProjectForScope(value any, scope string) (any, error) {
	return ProjectForAction(value, scope, "")
}

func ProjectForAction(value any, scope, action string) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	return projectValue(normalized, "", scope, action)
}

func ProjectMap(value map[string]any) (map[string]any, error) {
	return ProjectMapForScope(value, "")
}

func ProjectMapForScope(value map[string]any, scope string) (map[string]any, error) {
	return ProjectMapForAction(value, scope, "")
}

func ProjectMapForAction(value map[string]any, scope, action string) (map[string]any, error) {
	projected, err := ProjectForAction(value, scope, action)
	if err != nil {
		return nil, err
	}
	result, ok := projected.(map[string]any)
	if !ok {
		return nil, errors.New("projected public value is not an object")
	}
	return result, nil
}

func CompactGitFingerprint(scope, full string) (string, error) {
	compact, changed, err := compactGitFingerprint(scope, full)
	if err != nil {
		return "", err
	}
	if !changed {
		return "", fmt.Errorf("invalid full Git object ID")
	}
	return compact, nil
}

func ResolveGitFingerprint(scope, fingerprint string, authoritative []string) (string, error) {
	if !validFingerprint(fingerprint, 8) || strings.ToLower(fingerprint) != fingerprint {
		return "", fmt.Errorf("Git fingerprint must be 8 lowercase hexadecimal characters")
	}
	gitFingerprints.RLock()
	if _, ambiguous := gitFingerprints.ambiguous[scope][fingerprint]; ambiguous {
		gitFingerprints.RUnlock()
		return "", fmt.Errorf("Git fingerprint is ambiguous")
	}
	full, ok := gitFingerprints.values[scope][fingerprint]
	gitFingerprints.RUnlock()
	if !ok {
		return "", fmt.Errorf("Git fingerprint is unknown or stale")
	}
	matches := false
	for _, candidate := range authoritative {
		if strings.ToLower(candidate) == full {
			matches = true
			break
		}
	}
	if !matches {
		return "", fmt.Errorf("Git fingerprint is unknown, stale, or ambiguous in authoritative state")
	}
	return full, nil
}

func projectValue(value any, field, scope, action string) (any, error) {
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current))
		for key, child := range current {
			if omitNonGitIdentifier(key, child) || omitEmptyGitFingerprint(key, child) {
				continue
			}
			projected, err := projectValue(child, key, scope, action)
			if err != nil {
				return nil, err
			}
			result[key] = projected
		}
		return result, nil
	case []any:
		result := make([]any, len(current))
		for index, child := range current {
			projected, err := projectValue(child, field, scope, action)
			if err != nil {
				return nil, err
			}
			result[index] = projected
		}
		return result, nil
	case string:
		if isGitTextField(field, action) {
			compacted, err := CompactGitText(scope, current)
			if err != nil {
				return nil, err
			}
			return compacted, nil
		}
		if normalizeField(field) == "operation_id" {
			const activationPrefix = "gateway-debug-activation-"
			if strings.HasPrefix(current, activationPrefix) {
				compact, changed, err := compactGitFingerprint(scope, strings.TrimPrefix(current, activationPrefix))
				if err != nil {
					return nil, err
				}
				if changed {
					return activationPrefix + compact, nil
				}
			}
		}
		if gitFingerprintField(field) {
			compact, _, err := compactGitFingerprint(scope, current)
			return compact, err
		}
		return current, nil
	default:
		return value, nil
	}
}

func isGitTextField(field, action string) bool {
	switch action {
	case "git_show":
		return normalizeField(field) == "text"
	case "git_diff", "git_worktree_diff", "code/diff":
		return normalizeField(field) == "diff"
	default:
		return false
	}
}

func CompactGitText(scope, text string) (string, error) {
	lines := strings.SplitAfter(text, "\n")
	inShowHeader := true
	inDiff := false
	for index, line := range lines {
		body := strings.TrimSuffix(line, "\n")
		if inShowHeader {
			if body == "" {
				inShowHeader = false
			} else {
				compacted, err := compactGitShowHeaderLine(scope, body)
				if err != nil {
					return "", err
				}
				if compacted != body {
					lines[index] = compacted + line[len(body):]
					line = lines[index]
				}
			}
		}
		if strings.HasPrefix(body, "diff --git ") {
			inShowHeader = false
			inDiff = true
		}
		if inDiff && strings.HasPrefix(body, "index ") {
			compacted, err := compactGitDiffIndexLine(scope, body)
			if err != nil {
				return "", err
			}
			if compacted != body {
				newline := ""
				if strings.HasSuffix(line, "\n") {
					newline = "\n"
				}
				lines[index] = compacted + newline
			}
		}
	}
	return strings.Join(lines, ""), nil
}

func compactGitShowHeaderLine(scope, line string) (string, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return line, nil
	}
	switch fields[0] {
	case "commit", "tree", "parent":
		compact, changed, err := compactGitFingerprint(scope, fields[1])
		if err != nil {
			return "", err
		}
		if changed {
			position := strings.Index(line, fields[1])
			return line[:position] + compact + line[position+len(fields[1]):], nil
		}
	case "Merge:":
		for index := 1; index < len(fields); index++ {
			compact, changed, err := compactGitFingerprint(scope, fields[index])
			if err != nil {
				return "", err
			}
			if changed {
				line = strings.Replace(line, fields[index], compact, 1)
			}
		}
	}
	return line, nil
}

func compactGitDiffIndexLine(scope, line string) (string, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return line, nil
	}
	pair := strings.SplitN(fields[1], "..", 2)
	if len(pair) != 2 {
		return line, nil
	}
	left, leftChanged, err := compactGitFingerprint(scope, pair[0])
	if err != nil {
		return "", err
	}
	right, rightChanged, err := compactGitFingerprint(scope, pair[1])
	if err != nil {
		return "", err
	}
	if !leftChanged && !rightChanged {
		return line, nil
	}
	fields[1] = left + ".." + right
	return strings.Join(fields, " "), nil
}

func compactGitFingerprint(scope, value string) (string, bool, error) {
	if validFingerprint(value, 8) {
		return strings.ToLower(value), false, nil
	}
	if !validFingerprint(value, 40) {
		return value, false, nil
	}
	full := strings.ToLower(value)
	handle := full[:8]
	gitFingerprints.Lock()
	defer gitFingerprints.Unlock()
	if _, exists := gitFingerprints.ambiguous[scope][handle]; exists {
		return "", false, fmt.Errorf("Git fingerprint %q is ambiguous in public scope", handle)
	}
	if gitFingerprints.values[scope] == nil {
		gitFingerprints.values[scope] = make(map[string]string)
	}
	if previous, exists := gitFingerprints.values[scope][handle]; exists {
		if previous != full {
			delete(gitFingerprints.values[scope], handle)
			if gitFingerprints.ambiguous[scope] == nil {
				gitFingerprints.ambiguous[scope] = make(map[string]struct{})
			}
			gitFingerprints.ambiguous[scope][handle] = struct{}{}
			return "", false, fmt.Errorf("Git fingerprint %q is ambiguous in public scope", handle)
		}
		return handle, true, nil
	}
	if gitFingerprints.counts[scope] >= gitFingerprintCapacity {
		return "", false, fmt.Errorf("public Git fingerprint registry capacity reached")
	}
	gitFingerprints.values[scope][handle] = full
	gitFingerprints.counts[scope]++
	return handle, true, nil
}

func validFingerprint(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for i := range value {
		if !((value[i] >= '0' && value[i] <= '9') || (value[i] >= 'a' && value[i] <= 'f') || (value[i] >= 'A' && value[i] <= 'F')) {
			return false
		}
	}
	return true
}

func omitEmptyGitFingerprint(field string, value any) bool {
	text, ok := value.(string)
	return ok && text == "" && gitFingerprintField(field)
}

func omitNonGitIdentifier(field string, value any) bool {
	field = normalizeField(field)
	if field == "file_hash" {
		hash, ok := value.(string)
		return !ok || !validFingerprint(hash, 8)
	}
	if field == "gate_identity" || field == "task_digest" || field == "gate_profile" || field == "sha256" ||
		field == "hash" || strings.HasSuffix(field, "_sha256") || strings.HasSuffix(field, "_digest") || strings.HasSuffix(field, "_digests") ||
		strings.HasSuffix(field, "_hash") || strings.HasSuffix(field, "_hashes") || strings.HasSuffix(field, "_fingerprint") || strings.HasSuffix(field, "_fingerprints") {
		return true
	}
	if field != "digest" {
		return false
	}
	digest, ok := value.(string)
	return !ok || !validFingerprint(digest, 8) || strings.ToLower(digest) != digest
}

func gitFingerprintField(field string) bool {
	field = normalizeField(field)
	switch field {
	case "base", "commit", "head", "merge_base", "object_name", "parents", "revision", "sha", "tree", "tree_id":
		return true
	default:
		return strings.HasSuffix(field, "_base") || strings.HasSuffix(field, "_commit") ||
			strings.HasSuffix(field, "_head") || strings.HasSuffix(field, "_revision") || strings.HasSuffix(field, "_sha") ||
			strings.HasSuffix(field, "_tree")
	}
}

func normalizeField(field string) string {
	var normalized strings.Builder
	for i := 0; i < len(field); i++ {
		current := field[i]
		if current >= 'A' && current <= 'Z' {
			if i > 0 {
				previous := field[i-1]
				nextIsLower := i+1 < len(field) && field[i+1] >= 'a' && field[i+1] <= 'z'
				previousIsLowerOrDigit := (previous >= 'a' && previous <= 'z') || (previous >= '0' && previous <= '9')
				previousIsUpper := previous >= 'A' && previous <= 'Z'
				if previousIsLowerOrDigit || (previousIsUpper && nextIsLower) {
					normalized.WriteByte('_')
				}
			}
			normalized.WriteByte(current + ('a' - 'A'))
			continue
		}
		normalized.WriteByte(current)
	}
	return normalized.String()
}
