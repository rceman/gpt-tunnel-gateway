package onboarding

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
)

const preparedJournalMaxBytes int64 = 1 << 20

// ErrPreparedJournalNotFound indicates that the canonical prepared journal
// does not exist yet.
var ErrPreparedJournalNotFound = errors.New("prepared onboarding journal not found")

// PreparedJournalWriteReceipt reports the public result of a prepared journal
// write without exposing temporary files or other host internals.
type PreparedJournalWriteReceipt struct {
	OperationID   string
	JournalPath   string
	ReceiptSHA256 string
	Created       bool
}

// HubCommittedJournalWriteReceipt reports the durable journal transition
// without exposing local temporary paths beyond the canonical journal path.
type HubCommittedJournalWriteReceipt struct {
	OperationID   string
	JournalPath   string
	ReceiptSHA256 string
	Created       bool
}

var writeOnboardingJournalAtomic = fsutil.WriteFileAtomic

// PreparedJournalPath returns the only supported local journal path for an
// onboarding operation.
func PreparedJournalPath(stateDir, operationID string) (string, error) {
	if err := validatePreparedJournalStateDir(stateDir); err != nil {
		return "", err
	}
	if err := validatePreparedJournalOperationID(operationID); err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "onboarding", operationID+".json"), nil
}

// LoadPreparedJournal loads and strictly decodes the canonical prepared
// journal. It does not acquire the writer lock because the journal is replaced
// atomically and callers can use the returned receipt as a snapshot.
func LoadPreparedJournal(stateDir, operationID string) (Receipt, error) {
	path, err := PreparedJournalPath(stateDir, operationID)
	if err != nil {
		return Receipt{}, err
	}
	return readPreparedJournal(path, operationID, stateDir)
}

// LoadOnboardingJournal loads either the prepared or hub-committed state.
// Later activation states are intentionally rejected until their owning
// transaction is defined by a later protocol.
func LoadOnboardingJournal(stateDir, operationID string) (Receipt, error) {
	path, err := PreparedJournalPath(stateDir, operationID)
	if err != nil {
		return Receipt{}, err
	}
	return readOnboardingJournal(path, operationID, stateDir)
}

// WritePreparedJournal validates and atomically persists a prepared Receipt.
// Repeating the exact same canonical payload is idempotent; a different
// payload for the same operation is rejected without rewriting the journal.
func WritePreparedJournal(stateDir string, request Request, receipt Receipt) (result PreparedJournalWriteReceipt, err error) {
	if stateDir != request.GatewayStateDir {
		return PreparedJournalWriteReceipt{}, errors.New("prepared journal state directory does not match request gateway_state_dir")
	}
	path, err := PreparedJournalPath(stateDir, receipt.OperationID)
	if err != nil {
		return PreparedJournalWriteReceipt{}, err
	}
	if err := ValidatePreparedReceipt(receipt, request); err != nil {
		return PreparedJournalWriteReceipt{}, err
	}
	canonical, err := CanonicalPreparedReceiptJSON(receipt, request)
	if err != nil {
		return PreparedJournalWriteReceipt{}, err
	}
	digest, err := PreparedReceiptDigest(receipt, request)
	if err != nil {
		return PreparedJournalWriteReceipt{}, err
	}
	fileBytes := append(append([]byte(nil), canonical...), '\n')

	operationLock, err := acquirePreparedJournalLock(stateDir, receipt.OperationID)
	if err != nil {
		return PreparedJournalWriteReceipt{}, err
	}
	defer func() {
		if releaseErr := operationLock.Release(); err == nil && releaseErr != nil {
			err = fmt.Errorf("release prepared journal lock: %w", releaseErr)
		}
	}()

	existing, existingErr := readPreparedJournal(path, receipt.OperationID, stateDir)
	if existingErr == nil {
		existingCanonical, err := CanonicalPreparedReceiptJSON(existing, request)
		if err != nil {
			return PreparedJournalWriteReceipt{}, err
		}
		existingDigest, err := PreparedReceiptDigest(existing, request)
		if err != nil {
			return PreparedJournalWriteReceipt{}, err
		}
		if !bytes.Equal(existingCanonical, canonical) || existingDigest != digest {
			return PreparedJournalWriteReceipt{}, errors.New("ONBOARDING_OPERATION_CONFLICT")
		}
		return PreparedJournalWriteReceipt{
			OperationID:   receipt.OperationID,
			JournalPath:   path,
			ReceiptSHA256: existingDigest,
			Created:       false,
		}, nil
	}
	if !errors.Is(existingErr, ErrPreparedJournalNotFound) {
		return PreparedJournalWriteReceipt{}, existingErr
	}

	if err := fsutil.WriteFileAtomic(path, fileBytes, 0o600); err != nil {
		return PreparedJournalWriteReceipt{}, err
	}
	loaded, verifyErr := readPreparedJournal(path, receipt.OperationID, stateDir)
	if verifyErr != nil {
		return PreparedJournalWriteReceipt{}, removePreparedJournalAfterFailure(path, verifyErr)
	}
	verifiedCanonical, verifyErr := CanonicalPreparedReceiptJSON(loaded, request)
	if verifyErr != nil {
		return PreparedJournalWriteReceipt{}, removePreparedJournalAfterFailure(path, verifyErr)
	}
	verifiedDigest, verifyErr := PreparedReceiptDigest(loaded, request)
	if verifyErr != nil {
		return PreparedJournalWriteReceipt{}, removePreparedJournalAfterFailure(path, verifyErr)
	}
	if !bytes.Equal(verifiedCanonical, canonical) || verifiedDigest != digest {
		return PreparedJournalWriteReceipt{}, removePreparedJournalAfterFailure(path, errors.New("prepared journal verification mismatch"))
	}
	return PreparedJournalWriteReceipt{
		OperationID:   receipt.OperationID,
		JournalPath:   path,
		ReceiptSHA256: digest,
		Created:       true,
	}, nil
}

// WriteHubCommittedJournal durably advances a prepared journal after a
// successful Hub transaction. Exact retries are idempotent; a different
// operation payload is rejected without rewriting the journal.
func WriteHubCommittedJournal(stateDir string, request Request, receipt Receipt) (result HubCommittedJournalWriteReceipt, err error) {
	if stateDir != request.GatewayStateDir {
		return HubCommittedJournalWriteReceipt{}, errors.New("hub-committed journal state directory does not match request gateway_state_dir")
	}
	lock, err := acquirePreparedJournalLock(stateDir, receipt.OperationID)
	if err != nil {
		return HubCommittedJournalWriteReceipt{}, err
	}
	defer func() {
		if releaseErr := lock.Release(); err == nil && releaseErr != nil {
			err = fmt.Errorf("release onboarding journal lock: %w", releaseErr)
		}
	}()
	return writeHubCommittedJournalLocked(stateDir, request, receipt)
}

func writeHubCommittedJournalLocked(stateDir string, request Request, receipt Receipt) (result HubCommittedJournalWriteReceipt, err error) {
	path, err := PreparedJournalPath(stateDir, receipt.OperationID)
	if err != nil {
		return HubCommittedJournalWriteReceipt{}, err
	}
	if err := ValidateHubCommittedReceipt(receipt, request); err != nil {
		return HubCommittedJournalWriteReceipt{}, err
	}
	canonical, err := CanonicalHubCommittedReceiptJSON(receipt, request)
	if err != nil {
		return HubCommittedJournalWriteReceipt{}, err
	}
	digest, err := HubCommittedReceiptDigest(receipt, request)
	if err != nil {
		return HubCommittedJournalWriteReceipt{}, err
	}
	fileBytes := append(append([]byte(nil), canonical...), '\n')
	existing, existingErr := readOnboardingJournal(path, receipt.OperationID, stateDir)
	if existingErr == nil {
		switch existing.State {
		case StateHubCommitted:
			existingCanonical, err := CanonicalHubCommittedReceiptJSON(existing, request)
			if err != nil {
				return HubCommittedJournalWriteReceipt{}, err
			}
			existingDigest, err := HubCommittedReceiptDigest(existing, request)
			if err != nil {
				return HubCommittedJournalWriteReceipt{}, err
			}
			if !bytes.Equal(existingCanonical, canonical) || existingDigest != digest {
				return HubCommittedJournalWriteReceipt{}, errors.New("ONBOARDING_OPERATION_CONFLICT")
			}
			return HubCommittedJournalWriteReceipt{OperationID: receipt.OperationID, JournalPath: path, ReceiptSHA256: existingDigest, Created: false}, nil
		case StatePrepared:
			if err := ValidatePreparedReceipt(existing, request); err != nil {
				return HubCommittedJournalWriteReceipt{}, err
			}
		default:
			return HubCommittedJournalWriteReceipt{}, fmt.Errorf("onboarding journal has unsupported state %q", existing.State)
		}
	} else if !errors.Is(existingErr, ErrPreparedJournalNotFound) {
		return HubCommittedJournalWriteReceipt{}, existingErr
	}
	if err := writeOnboardingJournalAtomic(path, fileBytes, 0o600); err != nil {
		return HubCommittedJournalWriteReceipt{}, err
	}
	loaded, verifyErr := readOnboardingJournal(path, receipt.OperationID, stateDir)
	if verifyErr != nil {
		return HubCommittedJournalWriteReceipt{}, verifyErr
	}
	verifiedCanonical, verifyErr := CanonicalHubCommittedReceiptJSON(loaded, request)
	if verifyErr != nil {
		return HubCommittedJournalWriteReceipt{}, verifyErr
	}
	verifiedDigest, verifyErr := HubCommittedReceiptDigest(loaded, request)
	if verifyErr != nil {
		return HubCommittedJournalWriteReceipt{}, verifyErr
	}
	if loaded.State != StateHubCommitted || !bytes.Equal(verifiedCanonical, canonical) || verifiedDigest != digest {
		return HubCommittedJournalWriteReceipt{}, errors.New("hub-committed journal verification mismatch")
	}
	return HubCommittedJournalWriteReceipt{OperationID: receipt.OperationID, JournalPath: path, ReceiptSHA256: digest, Created: true}, nil
}

const (
	preparedJournalLockAttempts = 200
	preparedJournalLockDelay    = 5 * time.Millisecond
)

func acquirePreparedJournalLock(stateDir, operationID string) (*lockfile.Lock, error) {
	var lastErr error
	for attempt := 0; attempt < preparedJournalLockAttempts; attempt++ {
		operationLock, err := lockfile.Acquire(filepath.Join(stateDir, "locks"), operationID)
		if err == nil {
			return operationLock, nil
		}
		lastErr = err
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return nil, err
		}
		if attempt+1 < preparedJournalLockAttempts {
			time.Sleep(preparedJournalLockDelay)
		}
	}
	return nil, fmt.Errorf("acquire prepared journal lock after bounded contention retry: %w", lastErr)
}

func validatePreparedJournalStateDir(stateDir string) error {
	if stateDir == "" {
		return errors.New("state directory must not be empty")
	}
	if strings.ContainsAny(stateDir, "\x00\r\n") {
		return errors.New("state directory contains a forbidden character")
	}
	if !filepath.IsAbs(stateDir) {
		return errors.New("state directory must be absolute")
	}
	if filepath.Clean(stateDir) != stateDir {
		return errors.New("state directory must be clean")
	}
	return nil
}

func validatePreparedJournalOperationID(operationID string) error {
	if !receiptUUIDPattern.MatchString(operationID) {
		return errors.New("operation ID must be a lowercase canonical UUID")
	}
	return nil
}

func readPreparedJournal(path, operationID, stateDir string) (Receipt, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Receipt{}, fmt.Errorf("%w: %s", ErrPreparedJournalNotFound, path)
		}
		return Receipt{}, err
	}
	if !info.Mode().IsRegular() {
		return Receipt{}, fmt.Errorf("prepared journal is not a regular file: %s", path)
	}
	if info.Mode().Perm() != 0o600 {
		return Receipt{}, fmt.Errorf("prepared journal must have mode 0600: %s", path)
	}
	if info.Size() > preparedJournalMaxBytes {
		return Receipt{}, fmt.Errorf("prepared journal exceeds %d bytes: %s", preparedJournalMaxBytes, path)
	}
	data, err := fsutil.ReadFileBounded(path, preparedJournalMaxBytes)
	if err != nil {
		return Receipt{}, err
	}
	receipt, err := DecodeReceipt(data)
	if err != nil {
		return Receipt{}, err
	}
	if receipt.OperationID != operationID {
		return Receipt{}, errors.New("prepared journal operation ID does not match its path")
	}
	if receipt.State != StatePrepared {
		return Receipt{}, fmt.Errorf("prepared journal has unsupported state %q", receipt.State)
	}
	canonical, err := json.Marshal(receipt)
	if err != nil {
		return Receipt{}, fmt.Errorf("canonicalize prepared journal: %w", err)
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(data, canonical) {
		return Receipt{}, errors.New("prepared journal is not canonical")
	}
	if err := ValidatePreparedReceiptIntrinsic(receipt); err != nil {
		return Receipt{}, fmt.Errorf("invalid prepared journal receipt: %w", err)
	}
	if receipt.RepositoryProof.GatewayStateDir != stateDir {
		return Receipt{}, errors.New("prepared journal gateway_state_dir does not match state directory")
	}
	return receipt, nil
}

func readOnboardingJournal(path, operationID, stateDir string) (Receipt, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Receipt{}, fmt.Errorf("%w: %s", ErrPreparedJournalNotFound, path)
		}
		return Receipt{}, err
	}
	if !info.Mode().IsRegular() {
		return Receipt{}, fmt.Errorf("onboarding journal is not a regular file: %s", path)
	}
	if info.Mode().Perm() != 0o600 {
		return Receipt{}, fmt.Errorf("onboarding journal must have mode 0600: %s", path)
	}
	if info.Size() > preparedJournalMaxBytes {
		return Receipt{}, fmt.Errorf("onboarding journal exceeds %d bytes: %s", preparedJournalMaxBytes, path)
	}
	data, err := fsutil.ReadFileBounded(path, preparedJournalMaxBytes)
	if err != nil {
		return Receipt{}, err
	}
	receipt, err := DecodeReceipt(data)
	if err != nil {
		return Receipt{}, err
	}
	if receipt.OperationID != operationID {
		return Receipt{}, errors.New("onboarding journal operation ID does not match its path")
	}
	canonical, err := json.Marshal(receipt)
	if err != nil {
		return Receipt{}, fmt.Errorf("canonicalize onboarding journal: %w", err)
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(data, canonical) {
		return Receipt{}, errors.New("onboarding journal is not canonical")
	}
	switch receipt.State {
	case StatePrepared:
		if err := ValidatePreparedReceiptIntrinsic(receipt); err != nil {
			return Receipt{}, fmt.Errorf("invalid prepared journal receipt: %w", err)
		}
	case StateHubCommitted:
		if err := ValidateHubCommittedReceiptIntrinsic(receipt); err != nil {
			return Receipt{}, fmt.Errorf("invalid hub-committed journal receipt: %w", err)
		}
	default:
		return Receipt{}, fmt.Errorf("onboarding journal has unsupported state %q", receipt.State)
	}
	if receipt.RepositoryProof.GatewayStateDir != stateDir {
		return Receipt{}, errors.New("onboarding journal gateway_state_dir does not match state directory")
	}
	return receipt, nil
}

func removePreparedJournalAfterFailure(path string, cause error) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w; remove invalid prepared journal: %v", cause, err)
	}
	return cause
}
