package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

const maxLegacySessionFiles = 4096

const retiredLegacyDeliveryRole = "delivery"

func validateRetiredLegacyDelivery(record Record, id string) error {
	if record.ID != id || record.Role != retiredLegacyDeliveryRole {
		return fmt.Errorf("%w: invalid retired delivery session record", ErrInvalidSession)
	}
	prefix, _, ok := strings.Cut(id, "-")
	if !ok || (prefix != SessionIDPrefixLegacy && prefix != "SD") {
		return fmt.Errorf("%w: invalid retired delivery session identity", ErrInvalidSession)
	}
	return nil
}

// CutoverLegacyJSON imports the bounded legacy session directory after Local
// migrations. It is commit-before-cleanup and retry-idempotent: a crash after
// the SQLite commit leaves identical files that are safely removed next boot.
func CutoverLegacyJSON(ctx context.Context, stateDir string, db *sqlitestore.Databases) error {
	if db == nil || db.Local == nil {
		return fmt.Errorf("local session store is unavailable")
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Clean(stateDir), "sessions"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(entries) > maxLegacySessionFiles {
		return fmt.Errorf("legacy session cutover exceeds %d files", maxLegacySessionFiles)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if filepath.Ext(entry.Name()) != ".json" || !sessionIDRE.MatchString(id) {
			return fmt.Errorf("unexpected legacy session entry %q", entry.Name())
		}
		paths = append(paths, entry.Name())
	}
	sort.Strings(paths)
	type imported struct {
		name    string
		record  Record
		payload []byte
	}
	imports := make([]imported, 0, len(paths))
	validatedLegacyFiles := make([]string, 0, len(paths))
	for _, name := range paths {
		id := strings.TrimSuffix(name, ".json")
		path := filepath.Join(filepath.Clean(stateDir), "sessions", name)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("legacy session %s is not a regular file", id)
		}
		var record Record
		if err := fsutil.ReadJSONBounded(path, maxRecordBytes, &record); err != nil {
			return err
		}
		if record.ID != id {
			return fmt.Errorf("legacy session identity mismatch")
		}
		if err := validateRecordShape(record); err != nil {
			return err
		}
		if record.Role == retiredLegacyDeliveryRole {
			if err := validateRetiredLegacyDelivery(record, id); err != nil {
				return err
			}
			validatedLegacyFiles = append(validatedLegacyFiles, name)
			continue
		}
		if !validRole(record.Role) || !sessionIDMatchesRole(record.ID, record.Role) {
			return fmt.Errorf("%w: invalid session record", ErrInvalidSession)
		}
		validatedLegacyFiles = append(validatedLegacyFiles, name)
		payload, err := json.Marshal(record)
		if err != nil {
			return err
		}
		existing, readErr := db.ReadLocalSession(ctx, id)
		if readErr == nil {
			expectedUpdatedAt := record.UpdatedAt.UTC().Format(time.RFC3339Nano)
			if !bytes.Equal(existing.Payload, payload) || existing.ID != id || existing.Status != record.Status || existing.UpdatedAt != expectedUpdatedAt {
				return fmt.Errorf("legacy session %s conflicts with Local record", id)
			}
			continue
		}
		if !errors.Is(readErr, sqlitestore.ErrLocalSessionNotFound) {
			return fmt.Errorf("read Local session %s: %w", id, readErr)
		}
		imports = append(imports, imported{
			name:    name,
			record:  record,
			payload: payload,
		})
	}
	rows := make([]sqlitestore.LocalSession, 0, len(imports))
	for _, item := range imports {
		rows = append(rows, sqlitestore.LocalSession{ID: item.record.ID, Payload: item.payload, UpdatedAt: item.record.UpdatedAt.UTC().Format(time.RFC3339Nano), Status: item.record.Status})
	}
	if err := db.CreateLocalSessions(ctx, rows); err != nil {
		return fmt.Errorf("import legacy sessions: %w", err)
	}
	for _, name := range validatedLegacyFiles {
		if err := os.Remove(filepath.Join(filepath.Clean(stateDir), "sessions", name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
