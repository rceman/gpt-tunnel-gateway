package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func candidateTrainNumber(trainID string) uint64 {
	_, number, _ := model.ParseTrainV2ID(trainID)
	return number
}

func parseCodeSelector(selector string) (string, uint64, string, error) {
	if strings.HasPrefix(selector, "WT-MAIN-") && len(selector) == len("WT-MAIN-")+8 {
		prefix := selector[len("WT-MAIN-"):]
		if !validSelectorPrefix(prefix) {
			return "", 0, "", fmt.Errorf("invalid worktree selector")
		}
		return "main", 0, prefix, nil
	}
	if strings.HasPrefix(selector, "WT-FIX-") {
		rest := strings.TrimPrefix(selector, "WT-FIX-")
		separator := strings.LastIndexByte(rest, '-')
		if separator < 1 || separator == len(rest)-1 {
			return "", 0, "", fmt.Errorf("invalid worktree selector")
		}
		slug, prefix := rest[:separator], rest[separator+1:]
		if model.ValidateTaskSlug(slug) != nil || !validSelectorPrefix(prefix) {
			return "", 0, "", fmt.Errorf("invalid worktree selector")
		}
		return "hotfix", 0, slug, nil
	}
	if strings.HasPrefix(selector, "WT-TSK") {
		parts := strings.Split(strings.TrimPrefix(selector, "WT-TSK"), "-")
		if len(parts) != 2 || len(parts[1]) != 8 || !validSelectorPrefix(parts[1]) {
			return "", 0, "", fmt.Errorf("invalid worktree selector")
		}
		number, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil || number == 0 {
			return "", 0, "", fmt.Errorf("invalid worktree selector")
		}
		return "task", number, parts[1], nil
	}
	if !strings.HasPrefix(selector, "WT-TRN") {
		return "", 0, "", fmt.Errorf("invalid worktree selector")
	}
	parts := strings.Split(strings.TrimPrefix(selector, "WT-TRN"), "-")
	if len(parts) != 2 || len(parts[1]) != 8 || (len(parts[0]) > 1 && strings.HasPrefix(parts[0], "0")) || !validSelectorPrefix(parts[1]) {
		return "", 0, "", fmt.Errorf("invalid worktree selector")
	}
	number, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return "", 0, "", err
	}
	return "train", number, parts[1], nil
}

func validSelectorPrefix(prefix string) bool {
	if len(prefix) != 8 {
		return false
	}
	for _, char := range prefix {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}
