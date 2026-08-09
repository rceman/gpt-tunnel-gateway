package onboarding

import (
	"errors"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
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

const (
	preparedJournalLockAttempts = 200
	preparedJournalLockDelay    = 5 * time.Millisecond
)
