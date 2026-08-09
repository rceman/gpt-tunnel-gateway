package model

import (
	"fmt"
	"strconv"
)

func ValidateProjectIdentifier(s string) error {
	if !idRE.MatchString(s) {
		return fmt.Errorf("invalid project identifier")
	}
	return nil
}

func ValidateProjectCode(s string) error {
	if !projectCodeRE.MatchString(s) {
		return fmt.Errorf("project_code must be exactly three uppercase letters")
	}
	return nil
}

func ValidateCompactIDNumber(n uint64) error {
	if n < 1 || n > MaxSafeInteger {
		return fmt.Errorf("compact identifier number must be between 1 and %d", MaxSafeInteger)
	}
	return nil
}

func ValidateProjectIdentifiers(v ProjectIdentifiers) error {
	if v.SchemaVersion != SchemaVersion {
		return fmt.Errorf("invalid project identifiers schema_version")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return err
	}
	if err := ValidateProjectCode(v.ProjectCode); err != nil {
		return err
	}
	if err := ValidateCompactIDNumber(v.NextTaskNumber); err != nil {
		return fmt.Errorf("next_task_number: %w", err)
	}
	if err := ValidateCompactIDNumber(v.NextADRNumber); err != nil {
		return fmt.Errorf("next_adr_number: %w", err)
	}
	return nil
}

func FormatTaskID(projectCode string, number uint64) (string, error) {
	if err := ValidateProjectCode(projectCode); err != nil {
		return "", err
	}
	if err := ValidateCompactIDNumber(number); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-TSK%d", projectCode, number), nil
}

func ParseTaskID(value string) (string, uint64, error) {
	matches := canonicalTaskIDRE.FindStringSubmatch(value)
	if len(matches) != 3 {
		return "", 0, fmt.Errorf("invalid canonical task ID")
	}
	number, err := parseCompactIDNumber(matches[2])
	if err != nil {
		return "", 0, err
	}
	return matches[1], number, nil
}

// ParseHistoricalTaskID is for exact read-only decoding of pre-cutover task
// records. Operational creation and mutation must use ParseTaskID.
func ParseHistoricalTaskID(value string) (string, uint64, error) {
	matches := legacyTaskIDRE.FindStringSubmatch(value)
	if len(matches) != 3 {
		return "", 0, fmt.Errorf("invalid historical task ID")
	}
	number, err := parseCompactIDNumber(matches[2])
	if err != nil {
		return "", 0, err
	}
	return matches[1], number, nil
}

func ValidateTaskIDForProject(value, expectedProjectCode string) error {
	_, err := ParseTaskIDForProject(value, expectedProjectCode)
	return err
}

func ParseTaskIDForProject(value, expectedProjectCode string) (uint64, error) {
	if err := ValidateProjectCode(expectedProjectCode); err != nil {
		return 0, fmt.Errorf("expected project code: %w", err)
	}
	projectCode, number, err := ParseTaskID(value)
	if err != nil {
		return 0, err
	}
	if projectCode != expectedProjectCode {
		return 0, fmt.Errorf("compact task ID project code %q does not match expected project code %q", projectCode, expectedProjectCode)
	}
	return number, nil
}

func FormatRunID(taskID string, number uint64) (string, error) {
	if _, _, err := ParseTaskID(taskID); err != nil {
		return "", fmt.Errorf("task ID: %w", err)
	}
	if err := ValidateCompactIDNumber(number); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-RUN%d", taskID, number), nil
}

func ParseRunID(value string) (string, uint64, error) {
	matches := canonicalRunIDRE.FindStringSubmatch(value)
	if len(matches) != 4 {
		return "", 0, fmt.Errorf("invalid canonical run ID")
	}
	if _, _, err := ParseTaskID(matches[1]); err != nil {
		return "", 0, err
	}
	number, err := parseCompactIDNumber(matches[3])
	if err != nil {
		return "", 0, err
	}
	return matches[1], number, nil
}

func ParseHistoricalRunID(value string) (string, uint64, error) {
	matches := legacyRunIDRE.FindStringSubmatch(value)
	if len(matches) != 3 {
		return "", 0, fmt.Errorf("invalid historical run ID")
	}
	if _, _, err := ParseHistoricalTaskID(matches[1]); err != nil {
		return "", 0, err
	}
	number, err := parseCompactIDNumber(matches[2])
	if err != nil {
		return "", 0, err
	}
	return matches[1], number, nil
}

func ValidateRunIDForProject(value, expectedProjectCode string) error {
	_, _, err := ParseRunIDForProject(value, expectedProjectCode)
	return err
}

func ParseRunIDForProject(value, expectedProjectCode string) (string, uint64, error) {
	if err := ValidateProjectCode(expectedProjectCode); err != nil {
		return "", 0, fmt.Errorf("expected project code: %w", err)
	}
	taskID, number, err := ParseRunID(value)
	if err != nil {
		return "", 0, err
	}
	projectCode, _, err := ParseTaskID(taskID)
	if err != nil {
		return "", 0, err
	}
	if projectCode != expectedProjectCode {
		return "", 0, fmt.Errorf("run ID project code %q does not match expected project code %q", projectCode, expectedProjectCode)
	}
	return taskID, number, nil
}

func FormatADRID(projectCode string, number uint64) (string, error) {
	if err := ValidateProjectCode(projectCode); err != nil {
		return "", err
	}
	if err := ValidateCompactIDNumber(number); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-ADR%d", projectCode, number), nil
}

func ParseADRID(value string) (string, uint64, error) {
	matches := canonicalADRIDRE.FindStringSubmatch(value)
	if len(matches) != 3 {
		return "", 0, fmt.Errorf("invalid canonical ADR ID")
	}
	number, err := parseCompactIDNumber(matches[2])
	if err != nil {
		return "", 0, err
	}
	return matches[1], number, nil
}

func ParseHistoricalADRID(value string) (string, uint64, error) {
	matches := legacyADRIDRE.FindStringSubmatch(value)
	if len(matches) != 3 {
		return "", 0, fmt.Errorf("invalid historical ADR ID")
	}
	number, err := parseCompactIDNumber(matches[2])
	if err != nil {
		return "", 0, err
	}
	return matches[1], number, nil
}

func ValidateADRIDForProject(value, expectedProjectCode string) error {
	_, err := ParseADRIDForProject(value, expectedProjectCode)
	return err
}

func ParseADRIDForProject(value, expectedProjectCode string) (uint64, error) {
	if err := ValidateProjectCode(expectedProjectCode); err != nil {
		return 0, fmt.Errorf("expected project code: %w", err)
	}
	projectCode, number, err := ParseADRID(value)
	if err != nil {
		return 0, err
	}
	if projectCode != expectedProjectCode {
		return 0, fmt.Errorf("compact ADR ID project code %q does not match expected project code %q", projectCode, expectedProjectCode)
	}
	return number, nil
}

func parseCompactIDNumber(value string) (uint64, error) {
	number, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid compact identifier number")
	}
	if err := ValidateCompactIDNumber(number); err != nil {
		return 0, err
	}
	return number, nil
}
