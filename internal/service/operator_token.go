package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const OperatorTokenFile = "operator-token"

const operatorTokenBytes = 32

func OperatorTokenPath(stateDir string) string {
	return filepath.Join(stateDir, OperatorTokenFile)
}

func EnsureOperatorToken(stateDir string) (string, error) {
	if stateDir == "" {
		return "", fmt.Errorf("state directory is required for local operator credential")
	}
	path := OperatorTokenPath(stateDir)
	if token, err := readOperatorToken(path); err == nil {
		if err := os.Chmod(path, 0o600); err != nil {
			return "", fmt.Errorf("protect local operator credential: %w", err)
		}
		return token, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	raw := make([]byte, operatorTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate local operator credential: %w", err)
	}
	token := hex.EncodeToString(raw)
	tmp, err := os.CreateTemp(stateDir, ".operator-token-*")
	if err != nil {
		return "", fmt.Errorf("create local operator credential: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("protect local operator credential: %w", err)
	}
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write local operator credential: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("sync local operator credential: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("write local operator credential: %w", err)
	}
	if err := os.Link(tmpPath, path); err != nil {
		if os.IsExist(err) {
			token, readErr := readOperatorToken(path)
			if readErr != nil {
				return "", readErr
			}
			if err := os.Chmod(path, 0o600); err != nil {
				return "", fmt.Errorf("protect local operator credential: %w", err)
			}
			return token, nil
		}
		return "", fmt.Errorf("install local operator credential: %w", err)
	}
	if dir, err := os.Open(stateDir); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return token, nil
}

func ReadOperatorToken(stateDir string) (string, error) {
	if stateDir == "" {
		return "", fmt.Errorf("local operator credential is unavailable")
	}
	path := OperatorTokenPath(stateDir)
	token, err := readOperatorToken(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		return "", fmt.Errorf("local operator credential permissions are invalid")
	}
	return token, nil
}

func readOperatorToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", os.ErrNotExist
		}
		return "", fmt.Errorf("read local operator credential: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("local operator credential must be a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read local operator credential: %w", err)
	}
	token := strings.TrimSpace(string(data))
	decoded, decodeErr := hex.DecodeString(token)
	if len(decoded) != operatorTokenBytes || decodeErr != nil {
		return "", fmt.Errorf("local operator credential is invalid")
	}
	return token, nil
}

func (s *Service) ValidOperatorToken(token string) bool {
	if token == "" || strings.TrimSpace(token) != token || s == nil {
		return false
	}
	expected, err := ReadOperatorToken(s.Config.StateDir)
	if err != nil || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}
