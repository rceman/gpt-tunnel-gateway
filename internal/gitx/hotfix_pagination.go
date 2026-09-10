package gitx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
)

// ListHotfixIdentitiesPage traverses stable filenames and validates one
// bounded page without constructing the full inventory.
func (r Runner) ListHotfixIdentitiesPage(stateDir, projectID string, limit int, cursor string) ([]HotfixIdentity, pagination.PageInfo, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return nil, pagination.PageInfo{}, err
	}
	if limit < 1 || limit > pagination.MaxLimit {
		return nil, pagination.PageInfo{}, fmt.Errorf("invalid hotfix identity page limit")
	}
	if _, err := hotfixIdentityPath(stateDir, projectID, "placeholder"); err != nil {
		return nil, pagination.PageInfo{}, err
	}
	dir := filepath.Join(stateDir, "hotfix-identities", projectID)
	directory, err := os.Open(dir)
	if os.IsNotExist(err) {
		return nil, pagination.PageInfo{}, nil
	}
	if err != nil {
		return nil, pagination.PageInfo{}, err
	}
	defer directory.Close()
	kind, after := "hotfix_list:"+projectID, ""
	if cursor != "" {
		after, err = pagination.Decode(cursor, kind)
		if err != nil {
			return nil, pagination.PageInfo{}, err
		}
	}
	page := make([]HotfixIdentity, 0, limit)
	foundCursor := cursor == ""
	for {
		entries, readErr := directory.Readdir(128)
		if readErr != nil && readErr != io.EOF {
			return nil, pagination.PageInfo{}, readErr
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			slug := strings.TrimSuffix(entry.Name(), ".json")
			ref := "refs/heads/hotfix/" + slug
			identity, identityErr := r.ReadHotfixIdentity(stateDir, projectID, ref)
			if identityErr != nil {
				var raw HotfixIdentity
				if err := fsutil.ReadJSONBounded(filepath.Join(dir, entry.Name()), hotfixIdentityMaxBytes, &raw); err != nil {
					return nil, pagination.PageInfo{}, fmt.Errorf("read hotfix identity %s: %w", ref, err)
				}
				if raw.TaskID == "" {
					continue
				}
				return nil, pagination.PageInfo{}, fmt.Errorf("read hotfix identity %s: %w", ref, identityErr)
			}
			if !foundCursor {
				if ref == after {
					foundCursor = true
					continue
				}
				continue
			}
			if len(page) == limit {
				return page, pagination.PageInfo{HasMore: true, NextCursor: pagination.Encode(kind, page[len(page)-1].HotfixRef)}, nil
			}
			page = append(page, identity)
		}
		if readErr == io.EOF {
			break
		}
	}
	if cursor != "" && !foundCursor {
		return nil, pagination.PageInfo{}, fmt.Errorf("continuation cursor is no longer valid")
	}
	return page, pagination.PageInfo{}, nil
}
