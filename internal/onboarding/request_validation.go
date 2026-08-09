package onboarding

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func ValidateRequest(request Request) error {
	if request.SchemaVersion != 1 {
		return fmt.Errorf("schema_version must equal 1")
	}
	if err := validateProjectID(request.ProjectID, "project_id"); err != nil {
		return err
	}
	if err := validateAbsolutePath(request.Root, "root"); err != nil {
		return err
	}
	if err := validateRemote(request.Remote, "remote"); err != nil {
		return err
	}
	if err := validateRepositoryURL(request.RepositoryURL, "repository_url"); err != nil {
		return err
	}
	if err := validateBranch(request.DefaultBranch, "default_branch"); err != nil {
		return err
	}
	if request.Airelay.SessionRequired {
		if request.Airelay.SessionKey == nil {
			return fmt.Errorf("airelay.session_key is required when session_required is true")
		}
		if err := validateSessionKey(*request.Airelay.SessionKey, "airelay.session_key"); err != nil {
			return err
		}
	} else if request.Airelay.SessionKey != nil {
		return fmt.Errorf("airelay.session_key is forbidden when session_required is false")
	}
	if err := validateProjectCode(request.ProjectCode, "project_code"); err != nil {
		return err
	}
	if err := validateAbsolutePath(request.GatewayStateDir, "gateway_state_dir"); err != nil {
		return err
	}
	if request.Workflow != nil {
		if !workflowRepoPattern.MatchString(request.Workflow.Repository) || len(request.Workflow.Repository) > 255 {
			return fmt.Errorf("workflow.repository has an invalid format")
		}
		if err := validateSHA40(request.Workflow.Commit, "workflow.commit"); err != nil {
			return err
		}
	}
	if err := validateInitialPlan(request.InitialPlan, request.ProjectID); err != nil {
		return err
	}
	return validateSHA40(request.ExpectedHubRevision, "expected_hub_revision")
}

func validateInitialPlan(plan InitialPlan, projectID string) error {
	if plan.SchemaVersion != 2 {
		return fmt.Errorf("initial_plan.schema_version must equal 2")
	}
	if err := validateProjectID(plan.ProjectID, "initial_plan.project_id"); err != nil {
		return err
	}
	if plan.ProjectID != projectID {
		return fmt.Errorf("initial_plan.project_id must match project_id")
	}
	if err := validatePositiveInteger(plan.Revision, "initial_plan.revision"); err != nil {
		return err
	}
	if err := validateString(plan.Title, "initial_plan.title", 1, 300); err != nil {
		return err
	}
	if err := validateString(plan.Summary, "initial_plan.summary", 1, 500); err != nil {
		return err
	}
	if utf8.RuneCountInString(plan.CurrentObjective) > 20000 || strings.IndexByte(plan.CurrentObjective, 0) >= 0 {
		return fmt.Errorf("initial_plan.current_objective is invalid")
	}
	if plan.Queue == nil || len(plan.Queue) > 200 {
		return fmt.Errorf("initial_plan.queue must be an array of at most 200 entries")
	}
	seenQueue := make(map[string]struct{}, len(plan.Queue))
	for index, item := range plan.Queue {
		if err := validateObjectID(item, fmt.Sprintf("initial_plan.queue[%d]", index)); err != nil {
			return err
		}
		if _, exists := seenQueue[item]; exists {
			return fmt.Errorf("initial_plan.queue must contain unique values")
		}
		seenQueue[item] = struct{}{}
	}
	if plan.Sections == nil || len(plan.Sections) > 200 {
		return fmt.Errorf("initial_plan.sections must be an array of at most 200 entries")
	}
	seenSections := make(map[string]struct{}, len(plan.Sections))
	for index, section := range plan.Sections {
		name := fmt.Sprintf("initial_plan.sections[%d]", index)
		if err := validateObjectID(section.ID, name+".id"); err != nil {
			return err
		}
		if _, exists := seenSections[section.ID]; exists {
			return fmt.Errorf("initial_plan.sections must contain unique identifiers")
		}
		seenSections[section.ID] = struct{}{}
		if err := validateString(section.Title, name+".title", 1, 300); err != nil {
			return err
		}
		if err := validateString(section.ShortDescription, name+".short_description", 1, 500); err != nil {
			return err
		}
		if strings.ContainsAny(section.ShortDescription, "\x00\r\n") {
			return fmt.Errorf("%s.short_description contains a forbidden control character", name)
		}
		if err := validatePositiveInteger(section.Revision, name+".revision"); err != nil {
			return err
		}
	}
	if err := validateString(plan.UpdatedBy, "initial_plan.updated_by", 1, 255); err != nil {
		return err
	}
	if strings.ContainsAny(plan.UpdatedBy, "\x00\r\n") {
		return fmt.Errorf("initial_plan.updated_by contains a forbidden control character")
	}
	return validateDateTime(plan.UpdatedAt, "initial_plan.updated_at")
}

func validatePositiveInteger(value PositiveInteger, name string) error {
	if value < 1 || uint64(value) > MaxSafeInteger {
		return fmt.Errorf("%s must be between 1 and %d", name, MaxSafeInteger)
	}
	return nil
}

func validateString(value, name string, minimum, maximum int) error {
	length := utf8.RuneCountInString(value)
	if length < minimum || length > maximum {
		return fmt.Errorf("%s length is outside the allowed range", name)
	}
	return nil
}

func validateProjectID(value, name string) error {
	if !projectIDPattern.MatchString(value) {
		return fmt.Errorf("%s has an invalid format", name)
	}
	return nil
}

func validateObjectID(value, name string) error {
	if !objectIDPattern.MatchString(value) {
		return fmt.Errorf("%s has an invalid format", name)
	}
	return nil
}

func validateProjectCode(value, name string) error {
	if !projectCodePattern.MatchString(value) {
		return fmt.Errorf("%s has an invalid format", name)
	}
	return nil
}

func validateRemote(value, name string) error {
	if !remotePattern.MatchString(value) {
		return fmt.Errorf("%s has an invalid format", name)
	}
	return nil
}

func validateSessionKey(value, name string) error {
	if !sessionKeyPattern.MatchString(value) {
		return fmt.Errorf("%s has an invalid format", name)
	}
	return nil
}

func validateSHA40(value, name string) error {
	if !sha40Pattern.MatchString(value) {
		return fmt.Errorf("%s has an invalid format", name)
	}
	return nil
}

func validateAbsolutePath(value, name string) error {
	if utf8.RuneCountInString(value) < 2 || !strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, "//") || path.Clean(value) != value {
		return fmt.Errorf("%s must be a normalized absolute POSIX path", name)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("%s must be a normalized absolute POSIX path", name)
		}
	}
	resolved, err := realpathWithMissingSuffix(value)
	if err != nil || resolved != value {
		return fmt.Errorf("%s must be a normalized absolute POSIX path", name)
	}
	return nil
}

func validateRepositoryURL(value, name string) error {
	if utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > 2048 || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s contains surrounding whitespace or is too long", name)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f || unicode.IsSpace(character) {
			return fmt.Errorf("%s contains whitespace or control characters", name)
		}
	}
	if strings.HasPrefix(value, "/") {
		return validateAbsolutePath(value, name)
	}
	separator := strings.IndexByte(value, ':')
	if separator <= 0 || separator == len(value)-1 {
		return fmt.Errorf("%s must be an absolute path or a Git URL containing ':'", name)
	}
	return nil
}

func validateBranch(value, name string) error {
	if len(value) < 1 || len(value) > 255 || strings.HasPrefix(value, "-") || strings.Contains(value, "..") || strings.HasSuffix(value, "/") || strings.ContainsAny(value, " ~^:?*[]\\\x00\r\n") {
		return fmt.Errorf("%s is not a safe Git ref component", name)
	}
	return nil
}

func validateDateTime(value, name string) error {
	if value == "" || (!strings.Contains(value, "T") && !strings.Contains(value, "t")) {
		return fmt.Errorf("%s must include a timezone and time separator", name)
	}
	parseValue := value
	if len(parseValue) > 10 && parseValue[10] == 't' {
		parseValue = parseValue[:10] + "T" + parseValue[11:]
	}
	if _, err := time.Parse(time.RFC3339Nano, parseValue); err != nil {
		return fmt.Errorf("%s must be RFC3339 date-time", name)
	}
	return nil
}

func realpathWithMissingSuffix(value string) (string, error) {
	candidate := value
	suffix := make([]string, 0)
	for {
		_, err := os.Lstat(candidate)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				return "", err
			}
			parts := append([]string{resolved}, suffix...)
			return path.Clean(path.Join(parts...)), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := path.Dir(candidate)
		if parent == candidate {
			return "", fmt.Errorf("cannot resolve absolute path")
		}
		suffix = append([]string{path.Base(candidate)}, suffix...)
		candidate = parent
	}
}
