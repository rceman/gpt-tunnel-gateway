package onboarding

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

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

// WriteActivatedJournal durably advances a hub-committed receipt after local
// O3c activation. It is idempotent for the exact activated payload and never
// rewrites an already activated receipt with different evidence.
func WriteActivatedJournal(stateDir string, request Request, receipt Receipt) (result HubCommittedJournalWriteReceipt, err error) {
	if stateDir != request.GatewayStateDir {
		return HubCommittedJournalWriteReceipt{}, errors.New("activated journal state directory does not match request gateway_state_dir")
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
	return writeActivatedJournalLocked(stateDir, request, receipt)
}

func writeActivatedJournalLocked(stateDir string, request Request, receipt Receipt) (result HubCommittedJournalWriteReceipt, err error) {
	path, err := PreparedJournalPath(stateDir, receipt.OperationID)
	if err != nil {
		return HubCommittedJournalWriteReceipt{}, err
	}
	if err := ValidateActivatedReceipt(receipt, request); err != nil {
		return HubCommittedJournalWriteReceipt{}, err
	}
	canonical, err := CanonicalActivatedReceiptJSON(receipt, request)
	if err != nil {
		return HubCommittedJournalWriteReceipt{}, err
	}
	digest, err := ActivatedReceiptDigest(receipt, request)
	if err != nil {
		return HubCommittedJournalWriteReceipt{}, err
	}
	fileBytes := append(append([]byte(nil), canonical...), '\n')
	existing, existingErr := readOnboardingJournal(path, receipt.OperationID, stateDir)
	if existingErr == nil {
		switch existing.State {
		case StateActivated:
			existingCanonical, err := CanonicalActivatedReceiptJSON(existing, request)
			if err != nil {
				return HubCommittedJournalWriteReceipt{}, err
			}
			existingDigest, err := ActivatedReceiptDigest(existing, request)
			if err != nil {
				return HubCommittedJournalWriteReceipt{}, err
			}
			if !bytes.Equal(existingCanonical, canonical) || existingDigest != digest {
				return HubCommittedJournalWriteReceipt{}, errors.New("ONBOARDING_OPERATION_CONFLICT")
			}
			return HubCommittedJournalWriteReceipt{
				OperationID:   receipt.OperationID,
				JournalPath:   path,
				ReceiptSHA256: existingDigest,
				Created:       false,
			}, nil
		case StateHubCommitted, StateRecoveryRequired:
			if err := validateActivationTransition(existing, receipt, request); err != nil {
				return HubCommittedJournalWriteReceipt{}, err
			}
		default:
			return HubCommittedJournalWriteReceipt{}, fmt.Errorf("onboarding journal has unsupported transition state %q", existing.State)
		}
	} else if errors.Is(existingErr, ErrPreparedJournalNotFound) {
		return HubCommittedJournalWriteReceipt{}, fmt.Errorf("activation requires the prior hub-committed journal: %w", existingErr)
	} else {
		return HubCommittedJournalWriteReceipt{}, existingErr
	}
	if err := writeOnboardingJournalAtomic(path, fileBytes, 0o600); err != nil {
		return HubCommittedJournalWriteReceipt{}, err
	}
	loaded, verifyErr := readOnboardingJournal(path, receipt.OperationID, stateDir)
	if verifyErr != nil {
		return HubCommittedJournalWriteReceipt{}, verifyErr
	}
	verifiedCanonical, verifyErr := CanonicalActivatedReceiptJSON(loaded, request)
	if verifyErr != nil {
		return HubCommittedJournalWriteReceipt{}, verifyErr
	}
	verifiedDigest, verifyErr := ActivatedReceiptDigest(loaded, request)
	if verifyErr != nil {
		return HubCommittedJournalWriteReceipt{}, verifyErr
	}
	if loaded.State != StateActivated || !bytes.Equal(verifiedCanonical, canonical) || verifiedDigest != digest {
		return HubCommittedJournalWriteReceipt{}, errors.New("activated journal verification mismatch")
	}
	return HubCommittedJournalWriteReceipt{
		OperationID:   receipt.OperationID,
		JournalPath:   path,
		ReceiptSHA256: digest,
		Created:       true,
	}, nil
}

func writeRecoveryJournalLocked(stateDir string, request Request, receipt Receipt) (result HubCommittedJournalWriteReceipt, err error) {
	path, err := PreparedJournalPath(stateDir, receipt.OperationID)
	if err != nil {
		return HubCommittedJournalWriteReceipt{}, err
	}
	if err := ValidateRecoveryReceipt(receipt, request); err != nil {
		return HubCommittedJournalWriteReceipt{}, err
	}
	canonical, err := json.Marshal(receipt)
	if err != nil {
		return HubCommittedJournalWriteReceipt{}, fmt.Errorf("canonicalize recovery journal: %w", err)
	}
	digestBytes := sha256.Sum256(canonical)
	digest := hex.EncodeToString(digestBytes[:])
	fileBytes := append(append([]byte(nil), canonical...), '\n')
	existing, existingErr := readOnboardingJournal(path, receipt.OperationID, stateDir)
	if existingErr != nil {
		if errors.Is(existingErr, ErrPreparedJournalNotFound) {
			return HubCommittedJournalWriteReceipt{}, fmt.Errorf("recovery requires the prior hub-committed journal: %w", existingErr)
		}
		return HubCommittedJournalWriteReceipt{}, existingErr
	}
	if err := validateRecoveryTransition(existing, receipt, request); err != nil {
		return HubCommittedJournalWriteReceipt{}, err
	}
	existingCanonical, err := json.Marshal(existing)
	if err != nil {
		return HubCommittedJournalWriteReceipt{}, err
	}
	if existing.State == StateRecoveryRequired && bytes.Equal(existingCanonical, canonical) {
		existingDigest := sha256.Sum256(existingCanonical)
		return HubCommittedJournalWriteReceipt{
			OperationID:   receipt.OperationID,
			JournalPath:   path,
			ReceiptSHA256: hex.EncodeToString(existingDigest[:]),
			Created:       false,
		}, nil
	}
	if err := writeOnboardingJournalAtomic(path, fileBytes, 0o600); err != nil {
		return HubCommittedJournalWriteReceipt{}, err
	}
	loaded, verifyErr := readOnboardingJournal(path, receipt.OperationID, stateDir)
	if verifyErr != nil {
		return HubCommittedJournalWriteReceipt{}, verifyErr
	}
	verifiedCanonical, verifyErr := json.Marshal(loaded)
	if verifyErr != nil {
		return HubCommittedJournalWriteReceipt{}, verifyErr
	}
	if loaded.State != StateRecoveryRequired || !bytes.Equal(verifiedCanonical, canonical) {
		return HubCommittedJournalWriteReceipt{}, errors.New("recovery journal verification mismatch")
	}
	return HubCommittedJournalWriteReceipt{
		OperationID:   receipt.OperationID,
		JournalPath:   path,
		ReceiptSHA256: digest,
		Created:       true,
	}, nil
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
			return HubCommittedJournalWriteReceipt{
				OperationID:   receipt.OperationID,
				JournalPath:   path,
				ReceiptSHA256: existingDigest,
				Created:       false,
			}, nil
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
	return HubCommittedJournalWriteReceipt{
		OperationID:   receipt.OperationID,
		JournalPath:   path,
		ReceiptSHA256: digest,
		Created:       true,
	}, nil
}
