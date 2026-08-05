package onboarding

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const MaxSafeInteger uint64 = 9007199254740991

// PositiveInteger is a JSON Schema integer constrained to the positive,
// JavaScript-safe range used by the onboarding contract.
type PositiveInteger uint64

func (n *PositiveInteger) UnmarshalJSON(data []byte) error {
	value, err := parsePositiveInteger(data)
	if err != nil {
		return err
	}
	*n = PositiveInteger(value)
	return nil
}

type Request struct {
	SchemaVersion       PositiveInteger `json:"schema_version"`
	ProjectID           string          `json:"project_id"`
	Root                string          `json:"root"`
	Remote              string          `json:"remote"`
	RepositoryURL       string          `json:"repository_url"`
	DefaultBranch       string          `json:"default_branch"`
	Airelay             Airelay         `json:"airelay"`
	ProjectCode         string          `json:"project_code"`
	GatewayStateDir     string          `json:"gateway_state_dir"`
	Workflow            *Workflow       `json:"workflow,omitempty"`
	InitialPlan         InitialPlan     `json:"initial_plan"`
	ExpectedHubRevision string          `json:"expected_hub_revision"`
}

type Airelay struct {
	SessionRequired bool    `json:"session_required"`
	SessionKey      *string `json:"session_key,omitempty"`
}

type Workflow struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}

type InitialPlan struct {
	SchemaVersion    PositiveInteger      `json:"schema_version"`
	ProjectID        string               `json:"project_id"`
	Revision         PositiveInteger      `json:"revision"`
	Title            string               `json:"title"`
	Summary          string               `json:"summary"`
	CurrentObjective string               `json:"current_objective"`
	Queue            []string             `json:"queue"`
	Sections         []InitialPlanSection `json:"sections"`
	UpdatedBy        string               `json:"updated_by"`
	UpdatedAt        string               `json:"updated_at"`
}

type InitialPlanSection struct {
	ID               string          `json:"id"`
	Title            string          `json:"title"`
	ShortDescription string          `json:"short_description"`
	Revision         PositiveInteger `json:"revision"`
}

var (
	projectIDPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	objectIDPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	projectCodePattern  = regexp.MustCompile(`^[A-Z]{3}$`)
	remotePattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	sessionKeyPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	workflowRepoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	sha40Pattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func DecodeRequest(data []byte) (Request, error) {
	var request Request
	if err := decodeStrictObject(data, &request); err != nil {
		return Request{}, err
	}
	if err := ValidateRequest(request); err != nil {
		return Request{}, err
	}
	return request, nil
}

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

func CanonicalRequestJSON(request Request) ([]byte, error) {
	if err := ValidateRequest(request); err != nil {
		return nil, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical request: %w", err)
	}
	return data, nil
}

func RequestDigest(request Request) (string, error) {
	data, err := CanonicalRequestJSON(request)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func (request Request) CanonicalJSON() ([]byte, error) {
	return CanonicalRequestJSON(request)
}

func (request Request) Digest() (string, error) {
	return RequestDigest(request)
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
	if len(plan.CurrentObjective) > 20000 || strings.IndexByte(plan.CurrentObjective, 0) >= 0 {
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
	if len(value) < minimum || len(value) > maximum {
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
	if len(value) < 2 || !strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, "//") || path.Clean(value) != value {
		return fmt.Errorf("%s must be a normalized absolute POSIX path", name)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("%s must be a normalized absolute POSIX path", name)
		}
	}
	return nil
}

func validateRepositoryURL(value, name string) error {
	if len(value) < 1 || len(value) > 2048 || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s contains surrounding whitespace or is too long", name)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f || character == ' ' || character == '\t' {
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
	if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
		return fmt.Errorf("%s must be RFC3339 date-time", name)
	}
	return nil
}

func decodeStrictObject(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder, true); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return fmt.Errorf("trailing JSON content: %w", err)
		}
		return fmt.Errorf("trailing JSON content after %v", token)
	}

	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, root bool) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	switch value := token.(type) {
	case nil:
		return fmt.Errorf("null JSON values are not allowed")
	case json.Delim:
		switch value {
		case '{':
			if err := scanJSONObject(decoder); err != nil {
				return err
			}
		case '[':
			if root {
				return fmt.Errorf("request must be a JSON object")
			}
			if err := scanJSONArray(decoder); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", value)
		}
	default:
		if root {
			return fmt.Errorf("request must be a JSON object")
		}
	}
	return nil
}

func scanJSONObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("decode object key: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("object key is not a string")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate JSON field %q", key)
		}
		seen[key] = struct{}{}
		if err := scanJSONValue(decoder, false); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode object end: %w", err)
	}
	if end != json.Delim('}') {
		return fmt.Errorf("invalid JSON object termination")
	}
	return nil
}

func scanJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := scanJSONValue(decoder, false); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode array end: %w", err)
	}
	if end != json.Delim(']') {
		return fmt.Errorf("invalid JSON array termination")
	}
	return nil
}

func parsePositiveInteger(data []byte) (uint64, error) {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return 0, fmt.Errorf("positive integer is empty")
	}
	if text[0] == '-' {
		return 0, fmt.Errorf("positive integer cannot be negative")
	}
	index := 0
	integerStart := index
	if text[index] == '0' {
		index++
		if index < len(text) && text[index] >= '0' && text[index] <= '9' {
			return 0, fmt.Errorf("invalid JSON number")
		}
	} else if text[index] >= '1' && text[index] <= '9' {
		index++
		for index < len(text) && text[index] >= '0' && text[index] <= '9' {
			index++
		}
	} else {
		return 0, fmt.Errorf("positive integer must be a JSON number")
	}
	integerEnd := index
	fracLen := 0
	if index < len(text) && text[index] == '.' {
		index++
		fractionStart := index
		for index < len(text) && text[index] >= '0' && text[index] <= '9' {
			index++
		}
		fracLen = index - fractionStart
		if fracLen == 0 {
			return 0, fmt.Errorf("invalid JSON number fraction")
		}
	}
	exponentSign := 1
	exponent := uint64(0)
	if index < len(text) && (text[index] == 'e' || text[index] == 'E') {
		index++
		if index < len(text) && (text[index] == '+' || text[index] == '-') {
			if text[index] == '-' {
				exponentSign = -1
			}
			index++
		}
		exponentStart := index
		for index < len(text) && text[index] >= '0' && text[index] <= '9' {
			digit := uint64(text[index] - '0')
			if exponent > 1000000 || exponent > (1000000-digit)/10 {
				return 0, fmt.Errorf("JSON number exponent is outside the bounded range")
			}
			exponent = exponent*10 + digit
			index++
		}
		if index == exponentStart {
			return 0, fmt.Errorf("invalid JSON number exponent")
		}
	}
	if index != len(text) {
		return 0, fmt.Errorf("invalid JSON number")
	}

	digits := text[integerStart:integerEnd]
	if indexOfDot := strings.IndexByte(text[integerEnd:], '.'); indexOfDot >= 0 {
		fractionStart := integerEnd + indexOfDot + 1
		fractionEnd := fractionStart + fracLen
		digits += text[fractionStart:fractionEnd]
	}
	if strings.Trim(digits, "0") == "" {
		return 0, fmt.Errorf("positive integer must be greater than zero")
	}

	if exponentSign < 0 || exponent < uint64(fracLen) {
		decimalPlaces := uint64(fracLen)
		if exponentSign < 0 {
			decimalPlaces += exponent
		} else {
			decimalPlaces -= exponent
		}
		if decimalPlaces >= uint64(len(digits)) {
			return 0, fmt.Errorf("JSON number is fractional")
		}
		split := len(digits) - int(decimalPlaces)
		if strings.Trim(digits[split:], "0") != "" {
			return 0, fmt.Errorf("JSON number is fractional")
		}
		digits = digits[:split]
	} else {
		zeroes := exponent - uint64(fracLen)
		trimmed := strings.TrimLeft(digits, "0")
		if uint64(len(trimmed))+zeroes > uint64(len(strconv.FormatUint(MaxSafeInteger, 10))) {
			return 0, fmt.Errorf("positive integer exceeds %d", MaxSafeInteger)
		}
		digits = trimmed + strings.Repeat("0", int(zeroes))
	}

	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return 0, fmt.Errorf("positive integer must be greater than zero")
	}
	value, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || value > MaxSafeInteger {
		return 0, fmt.Errorf("positive integer exceeds %d", MaxSafeInteger)
	}
	return value, nil
}
