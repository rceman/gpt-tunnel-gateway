package onboarding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func CanonicalHubCommittedReceiptJSON(receipt Receipt, request Request) ([]byte, error) {
	if err := ValidateHubCommittedReceipt(receipt, request); err != nil {
		return nil, err
	}
	return json.Marshal(receipt)
}

func HubCommittedReceiptDigest(receipt Receipt, request Request) (string, error) {
	data, err := CanonicalHubCommittedReceiptJSON(receipt, request)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func CanonicalRecoveryReceiptJSON(receipt Receipt, request Request) ([]byte, error) {
	if err := ValidateRecoveryReceipt(receipt, request); err != nil {
		return nil, err
	}
	return json.Marshal(receipt)
}

func RecoveryReceiptDigest(receipt Receipt, request Request) (string, error) {
	data, err := CanonicalRecoveryReceiptJSON(receipt, request)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func CanonicalActivatedReceiptJSON(receipt Receipt, request Request) ([]byte, error) {
	if err := ValidateActivatedReceipt(receipt, request); err != nil {
		return nil, err
	}
	return json.Marshal(receipt)
}

func ActivatedReceiptDigest(receipt Receipt, request Request) (string, error) {
	data, err := CanonicalActivatedReceiptJSON(receipt, request)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
