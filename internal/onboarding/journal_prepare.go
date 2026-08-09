package onboarding

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
)

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
