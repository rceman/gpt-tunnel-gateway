package model

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func ValidateObjectIdentifier(s string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`).MatchString(s) {
		return fmt.Errorf("invalid object identifier")
	}
	return nil
}

func ValidateADRIdentifier(s string) error {
	if !adrIDRE.MatchString(s) {
		return fmt.Errorf("invalid ADR identifier")
	}
	return nil
}

func validateAnyADRIdentifier(s string) error {
	if ValidateADRIdentifier(s) == nil || ValidateCanonicalADRIdentifier(s) == nil {
		return nil
	}
	return fmt.Errorf("invalid ADR identifier")
}

func ValidateCanonicalTaskID(s string) error {
	_, _, err := ParseTaskID(s)
	return err
}

func ValidateCanonicalRunID(s string) error {
	_, _, err := ParseRunID(s)
	return err
}

func ValidateCanonicalADRIdentifier(s string) error {
	_, _, err := ParseADRID(s)
	return err
}

func ValidateTaskSlug(s string) error {
	if len(s) < 1 || len(s) > 80 || !regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`).MatchString(s) {
		return fmt.Errorf("invalid task slug")
	}
	return nil
}

func ValidateDurableIdentifier(s string) error {
	if ValidateCanonicalTaskID(s) == nil || ValidateCanonicalRunID(s) == nil || ValidateCanonicalADRIdentifier(s) == nil || ValidateOperatorEventID(s) == nil {
		return nil
	}
	return fmt.Errorf("invalid canonical durable identifier")
}

func ValidateBranch(s string) error {
	if s == "" || len(s) > 255 || strings.ContainsAny(s, "\x00\r\n ~^:?*[\\") || strings.HasPrefix(s, "-") || strings.Contains(s, "..") || strings.HasSuffix(s, "/") {
		return fmt.Errorf("invalid branch")
	}
	return nil
}

func ValidateRevision(s string) error {
	if s == "" || len(s) > 255 || strings.ContainsAny(s, "\x00\r\n ~^:?*[\\") || strings.HasPrefix(s, "-") || strings.Contains(s, "..") {
		return fmt.Errorf("invalid revision")
	}
	return nil
}

func ValidateRelativePath(p string) error {
	if p == "" || len(p) > 4096 || filepath.IsAbs(p) || strings.ContainsRune(p, 0) || strings.Contains(p, `\`) {
		return fmt.Errorf("invalid relative path")
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	first := strings.Split(clean, "/")[0]
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.EqualFold(first, ".git") {
		return fmt.Errorf("path escapes root")
	}
	return nil
}

func CanonicalStrings(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func sha256RE(s string) bool { return len(s) == 64 && regexp.MustCompile(`^[0-9a-f]+$`).MatchString(s) }
