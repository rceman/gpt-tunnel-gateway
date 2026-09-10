package service

import (
	"context"
	"fmt"
	"testing"
)

func installLocalCodeBehaviorDouble(t *testing.T, f localCodeFixture, files map[string]string) {
	t.Helper()
	target := localCodeTarget{
		CodeIdentity: CodeIdentity{
			Worktree:    "WT-DOUBLE-00000000",
			CurrentHead: "dddddddddddddddddddddddddddddddddddddddd",
			Live:        true,
		},
		ProjectWorktree: f.service.Config.Projects["example"],
		Kind:            "main",
	}
	f.service.codeTargetResolver = func(context.Context, string, string, bool) (localCodeTarget, error) {
		return target, nil
	}
	f.service.codePathWalker = func(_ context.Context, _ localCodeTarget, paths []string, _ string, include, exclude []string, visit func(string) error) error {
		selected, err := validateLocalCodePaths(paths, false)
		if err != nil {
			return err
		}
		if len(selected) == 0 {
			return fmt.Errorf("test code double requires explicit paths")
		}
		if err := validateCodePatterns(include); err != nil {
			return err
		}
		if err := validateCodePatterns(exclude); err != nil {
			return err
		}
		for _, pathName := range selected {
			if codePathMatches(pathName, include, exclude) {
				if _, ok := files[pathName]; !ok {
					return fmt.Errorf("test code double has no file %q", pathName)
				}
				if err := visit(pathName); err != nil {
					return err
				}
			}
		}
		return nil
	}
	f.service.codeFileReader = func(_ context.Context, _ localCodeTarget, pathName string) (string, error) {
		content, ok := files[pathName]
		if !ok {
			return "", fmt.Errorf("test code double has no file %q", pathName)
		}
		return content, nil
	}
}
