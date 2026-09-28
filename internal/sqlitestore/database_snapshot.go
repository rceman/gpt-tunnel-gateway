package sqlitestore

/*
#cgo pkg-config: sqlite3
#include <sqlite3.h>
#include <stdlib.h>
static int sqlite3_get_page_size(sqlite3 *db) {
  sqlite3_stmt *statement = NULL;
  int page_size = 0;
  if (sqlite3_prepare_v2(db, "PRAGMA page_size", -1, &statement, NULL) == SQLITE_OK && sqlite3_step(statement) == SQLITE_ROW) {
    page_size = sqlite3_column_int(statement, 0);
  }
  sqlite3_finalize(statement);
  return page_size;
}
*/
import "C"

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"
)

const databaseSnapshotManifestVersion = 1
const databaseSnapshotManifestLimit = 1 << 20
const databaseSnapshotMaxBytes int64 = 1 << 30

type databaseSnapshotManifest struct {
	Version   int               `json:"version"`
	Present   map[string]bool   `json:"present"`
	Checksums map[string]string `json:"checksums"`
}

type sqliteOnlineBackup struct {
	source      *C.sqlite3
	destination *C.sqlite3
	backup      *C.sqlite3_backup
}

func SnapshotDatabases(ctx context.Context, sourceStateDir, snapshotStateDir string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if sourceStateDir == "" || snapshotStateDir == "" {
		return fmt.Errorf("state directory is invalid")
	}
	sourceRoot, err := filepath.Abs(sourceStateDir)
	if err != nil {
		return fmt.Errorf("source state directory is invalid")
	}
	snapshotRoot, err := filepath.Abs(snapshotStateDir)
	if err != nil || filepath.Clean(sourceRoot) == filepath.Clean(snapshotRoot) {
		return fmt.Errorf("snapshot state directory is invalid")
	}
	if err := validateStateDirectory(sourceRoot, true); err != nil {
		return fmt.Errorf("source state directory is invalid: %w", err)
	}
	if err := ensurePrivateDirectory(snapshotRoot); err != nil {
		return err
	}
	sharedSnapshot, localSnapshot := Paths(snapshotRoot)
	for _, path := range []string{sharedSnapshot, sharedSnapshot + "-wal", sharedSnapshot + "-shm", localSnapshot, localSnapshot + "-wal", localSnapshot + "-shm", filepath.Join(snapshotRoot, "snapshot.json")} {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("snapshot destination already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	manifest := databaseSnapshotManifest{
		Version:   databaseSnapshotManifestVersion,
		Present:   map[string]bool{},
		Checksums: map[string]string{},
	}
	sharedSource, localSource := Paths(sourceRoot)
	for _, file := range []struct {
		name   string
		source string
		target string
	}{{"shared.db", sharedSource, sharedSnapshot}, {"local.db", localSource, localSnapshot}} {
		if err := validateStateDirectory(filepath.Dir(file.source), true); err != nil {
			return fmt.Errorf("source %s database directory is invalid: %w", file.name, err)
		}
		if err := validateStateDirectory(filepath.Dir(file.target), true); err != nil {
			return fmt.Errorf("snapshot %s database directory is invalid: %w", file.name, err)
		}
		info, err := os.Lstat(file.source)
		if errors.Is(err, os.ErrNotExist) {
			manifest.Present[file.name] = false
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect %s database: %w", file.name, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s database is not a regular file", file.name)
		}
		if err := validateDatabaseAndSidecars(file.source); err != nil {
			return fmt.Errorf("inspect %s database sidecars: %w", file.name, err)
		}
		if err := os.MkdirAll(filepath.Dir(file.target), 0o700); err != nil {
			return err
		}
		if err := backupSQLiteDatabase(ctx, file.source, file.target); err != nil {
			return fmt.Errorf("snapshot %s database: %w", file.name, err)
		}
		if err := validateDatabaseAndSidecars(file.target); err != nil {
			return fmt.Errorf("validate %s database snapshot: %w", file.name, err)
		}
		checksum, err := fileSHA256(file.target)
		if err != nil {
			return fmt.Errorf("verify %s database snapshot: %w", file.name, err)
		}
		manifest.Present[file.name] = true
		manifest.Checksums[file.name] = checksum
	}
	return writeSnapshotManifest(snapshotRoot, manifest)
}

func RestoreDatabases(snapshotStateDir, targetStateDir string) error {
	snapshotRoot, err := filepath.Abs(snapshotStateDir)
	if err != nil || snapshotStateDir == "" {
		return fmt.Errorf("snapshot state directory is invalid")
	}
	targetRoot, err := filepath.Abs(targetStateDir)
	if err != nil || targetStateDir == "" || filepath.Clean(snapshotRoot) == filepath.Clean(targetRoot) {
		return fmt.Errorf("target state directory is invalid")
	}
	if err := validateStateDirectory(targetRoot, true); err != nil {
		return fmt.Errorf("target state directory is invalid: %w", err)
	}
	manifest, err := readSnapshotManifest(snapshotRoot)
	if err != nil {
		return err
	}
	sharedSnapshot, localSnapshot := Paths(snapshotRoot)
	sharedTarget, localTarget := Paths(targetRoot)
	files := []struct {
		name   string
		source string
		target string
	}{{"shared.db", sharedSnapshot, sharedTarget}, {"local.db", localSnapshot, localTarget}}
	for _, file := range files {
		if err := validateStateDirectory(filepath.Dir(file.source), true); err != nil {
			return fmt.Errorf("snapshot %s database directory is invalid: %w", file.name, err)
		}
		if err := validateStateDirectory(filepath.Dir(file.target), true); err != nil {
			return fmt.Errorf("target %s database directory is invalid: %w", file.name, err)
		}
		if manifest.Present[file.name] {
			info, err := os.Lstat(file.source)
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("snapshot %s database is unavailable or unsafe", file.name)
			}
			if err := validateDatabaseAndSidecars(file.source); err != nil {
				return fmt.Errorf("snapshot %s database exceeds safety bounds", file.name)
			}
			checksum, err := fileSHA256(file.source)
			if err != nil || checksum != manifest.Checksums[file.name] {
				return fmt.Errorf("snapshot %s database checksum mismatch", file.name)
			}
		}
		if err := validateDatabaseAndSidecars(file.target); err != nil {
			return fmt.Errorf("validate target %s database: %w", file.name, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(sharedTarget), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(localTarget), 0o700); err != nil {
		return err
	}
	for _, file := range files {
		if err := removeDatabaseSidecars(file.target); err != nil {
			return fmt.Errorf("remove target %s database sidecars: %w", file.name, err)
		}
		if manifest.Present[file.name] {
			if err := copyFileAtomic(file.source, file.target); err != nil {
				return fmt.Errorf("restore %s database: %w", file.name, err)
			}
		} else if err := removeRegularFile(file.target); err != nil {
			return fmt.Errorf("restore absent %s database: %w", file.name, err)
		}
	}
	return nil
}

func backupSQLiteDatabase(ctx context.Context, sourcePath, destinationPath string) (retErr error) {
	if _, err := os.Lstat(destinationPath); err == nil {
		return fmt.Errorf("snapshot destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	backup, err := openSQLiteOnlineBackup(sourcePath, destinationPath)
	if err != nil {
		_ = os.Remove(destinationPath)
		_ = os.Remove(destinationPath + "-wal")
		_ = os.Remove(destinationPath + "-shm")
		return err
	}
	defer func() {
		if closeErr := backup.close(); closeErr != nil {
			retErr = errors.Join(retErr, closeErr)
		}
		if retErr != nil {
			_ = os.Remove(destinationPath)
			_ = os.Remove(destinationPath + "-wal")
			_ = os.Remove(destinationPath + "-shm")
		}
	}()
	pageSize := int64(C.sqlite3_get_page_size(backup.source))
	if pageSize <= 0 {
		return fmt.Errorf("SQLite page size is invalid")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		pageCount := int64(C.sqlite3_backup_pagecount(backup.backup))
		if pageCount < 0 || pageCount > databaseSnapshotMaxBytes/pageSize {
			return fmt.Errorf("database exceeds the snapshot size limit")
		}
		rc := C.sqlite3_backup_step(backup.backup, 128)
		switch rc {
		case C.SQLITE_DONE:
			if err := backup.close(); err != nil {
				backup = nil
				return err
			}
			backup = nil
			if err := os.Chmod(destinationPath, 0o600); err != nil {
				return err
			}
			return syncFileAndDirectory(destinationPath)
		case C.SQLITE_OK:
		case C.SQLITE_BUSY, C.SQLITE_LOCKED:
			timer := time.NewTimer(10 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		default:
			return backup.sqliteError("copy SQLite pages", rc)
		}
	}
}

func openSQLiteOnlineBackup(sourcePath, destinationPath string) (*sqliteOnlineBackup, error) {
	cSourcePath := C.CString(sourcePath)
	defer C.free(unsafe.Pointer(cSourcePath))
	cDestinationPath := C.CString(destinationPath)
	defer C.free(unsafe.Pointer(cDestinationPath))
	backup := &sqliteOnlineBackup{}
	if rc := C.sqlite3_open_v2(cSourcePath, &backup.source, C.SQLITE_OPEN_READONLY|C.SQLITE_OPEN_FULLMUTEX, nil); rc != C.SQLITE_OK {
		err := backup.sqliteError("open source database", rc)
		_ = backup.close()
		return nil, err
	}
	if rc := C.sqlite3_open_v2(cDestinationPath, &backup.destination, C.SQLITE_OPEN_READWRITE|C.SQLITE_OPEN_CREATE|C.SQLITE_OPEN_FULLMUTEX, nil); rc != C.SQLITE_OK {
		err := backup.sqliteError("open snapshot database", rc)
		_ = backup.close()
		return nil, err
	}
	C.sqlite3_busy_timeout(backup.source, 250)
	C.sqlite3_busy_timeout(backup.destination, 250)
	cMain := C.CString("main")
	defer C.free(unsafe.Pointer(cMain))
	backup.backup = C.sqlite3_backup_init(backup.destination, cMain, backup.source, cMain)
	if backup.backup == nil {
		err := backup.sqliteError("initialize SQLite snapshot", C.sqlite3_errcode(backup.destination))
		_ = backup.close()
		return nil, err
	}
	return backup, nil
}

func (b *sqliteOnlineBackup) sqliteError(operation string, rc C.int) error {
	message := "unknown SQLite error"
	if b.destination != nil {
		message = C.GoString(C.sqlite3_errmsg(b.destination))
	} else if b.source != nil {
		message = C.GoString(C.sqlite3_errmsg(b.source))
	}
	return fmt.Errorf("%s: sqlite rc=%d: %s", operation, int(rc), message)
}

func (b *sqliteOnlineBackup) close() error {
	if b == nil {
		return nil
	}
	var errs []error
	if b.backup != nil {
		if rc := C.sqlite3_backup_finish(b.backup); rc != C.SQLITE_OK {
			errs = append(errs, b.sqliteError("finish SQLite snapshot", rc))
		}
		b.backup = nil
	}
	if b.source != nil {
		if rc := C.sqlite3_close_v2(b.source); rc != C.SQLITE_OK {
			errs = append(errs, b.sqliteError("close source database", rc))
		}
		b.source = nil
	}
	if b.destination != nil {
		if rc := C.sqlite3_close_v2(b.destination); rc != C.SQLITE_OK {
			errs = append(errs, b.sqliteError("close snapshot database", rc))
		}
		b.destination = nil
	}
	return errors.Join(errs...)
}

func validateStateDirectory(path string, allowMissing bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && allowMissing {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("state directory is unavailable or unsafe")
	}
	return nil
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("snapshot directory is unavailable or unsafe")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("snapshot directory must be owner-only")
	}
	return nil
}

func writeSnapshotManifest(root string, manifest databaseSnapshotManifest) error {
	if _, err := os.Lstat(filepath.Join(root, "snapshot.json")); err == nil {
		return fmt.Errorf("snapshot manifest already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(root, ".snapshot-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	cleanup := func(primary error) error {
		_ = file.Close()
		_ = os.Remove(temporary)
		return primary
	}
	if err := file.Chmod(0o600); err != nil {
		return cleanup(err)
	}
	if _, err := file.Write(data); err != nil {
		return cleanup(err)
	}
	if err := file.Sync(); err != nil {
		return cleanup(err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, filepath.Join(root, "snapshot.json")); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncDirectory(root)
}

func readSnapshotManifest(root string) (databaseSnapshotManifest, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || rootInfo.Mode().Perm()&0o077 != 0 {
		return databaseSnapshotManifest{}, fmt.Errorf("snapshot directory is unavailable or unsafe")
	}
	path := filepath.Join(root, "snapshot.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > databaseSnapshotManifestLimit {
		return databaseSnapshotManifest{}, fmt.Errorf("snapshot manifest is unavailable or unsafe")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return databaseSnapshotManifest{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var manifest databaseSnapshotManifest
	if err := decoder.Decode(&manifest); err != nil {
		return databaseSnapshotManifest{}, fmt.Errorf("decode snapshot manifest: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return databaseSnapshotManifest{}, fmt.Errorf("snapshot manifest has trailing data")
	}
	if manifest.Version != databaseSnapshotManifestVersion || len(manifest.Present) != 2 || len(manifest.Checksums) > 2 {
		return databaseSnapshotManifest{}, fmt.Errorf("snapshot manifest is invalid")
	}
	for _, name := range []string{"shared.db", "local.db"} {
		if _, ok := manifest.Present[name]; !ok {
			return databaseSnapshotManifest{}, fmt.Errorf("snapshot manifest is incomplete")
		}
		checksum, ok := manifest.Checksums[name]
		if manifest.Present[name] != ok || ok && (len(checksum) != sha256.Size*2 || checksum != strings.ToLower(checksum)) {
			return databaseSnapshotManifest{}, fmt.Errorf("snapshot manifest checksum set is invalid")
		}
		if ok {
			if _, err := hex.DecodeString(checksum); err != nil {
				return databaseSnapshotManifest{}, fmt.Errorf("snapshot manifest checksum is invalid")
			}
		}
	}
	return manifest, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validateDatabaseAndSidecars(path string) error {
	totalBytes := int64(0)
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("database path is not a regular file")
		}
		if info.Size() < 0 || info.Size() > databaseSnapshotMaxBytes-totalBytes {
			return fmt.Errorf("database and sidecars exceed the snapshot size limit")
		}
		totalBytes += info.Size()
	}
	return nil
}

func removeDatabaseSidecars(path string) error {
	for _, candidate := range []string{path + "-wal", path + "-shm"} {
		if err := removeRegularFile(candidate); err != nil {
			return err
		}
	}
	return nil
}

func removeRegularFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to remove non-regular database path")
	}
	return os.Remove(path)
}

func copyFileAtomic(sourcePath, destinationPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	temporary, err := os.CreateTemp(filepath.Dir(destinationPath), ".database-restore-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := func(primary error) error {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		return primary
	}
	if err := temporary.Chmod(0o600); err != nil {
		return cleanup(err)
	}
	if _, err := io.Copy(temporary, source); err != nil {
		return cleanup(err)
	}
	if err := temporary.Sync(); err != nil {
		return cleanup(err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := os.Rename(temporaryPath, destinationPath); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return syncDirectory(filepath.Dir(destinationPath))
}

func syncFileAndDirectory(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
