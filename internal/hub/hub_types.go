package hub

import (
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

const (
	ProtocolRoot = "gpt-tunnel/v1"
	RemoteName   = "origin"
)

type Store struct {
	Config config.Config
}

type TransactionResult struct {
	Before string   `json:"before"`
	After  string   `json:"after"`
	Remote string   `json:"remote"`
	Branch string   `json:"branch"`
	Paths  []string `json:"paths"`
}

type Mutator func(worktree string) ([]string, error)

func ManagedRoot(c config.Config) string {
	return filepath.Join(c.StateDir, "hub", "repository")
}
func Timestamp() time.Time { return time.Now().UTC() }
