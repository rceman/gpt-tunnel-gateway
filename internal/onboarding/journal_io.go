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
	case StateActivated:
		if err := ValidateActivatedReceiptIntrinsic(receipt); err != nil {
			return Receipt{}, fmt.Errorf("invalid activated journal receipt: %w", err)
		}
	case StateRecoveryRequired:
		if err := validatePostHubReceiptIntrinsic(receipt, StateRecoveryRequired); err != nil {
			return Receipt{}, fmt.Errorf("invalid recovery-required journal receipt: %w", err)
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
