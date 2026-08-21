package onboarding

import (
	"context"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type ActivationResult struct {
	OperationID       string
	ProjectID         string
	State             ReceiptState
	ReceiptSHA256     string
	RegistryBefore    string
	RegistryAfter     string
	Mirror            MirrorProof
	JournalRepairOnly bool
}

type ActivationHooks struct {
	RegistryWrite func(string, string, config.ManagedProjectRegistry) (config.ManagedProjectRegistryWriteReceipt, error)
	Mirror        func(context.Context, config.ProjectConfig, string, string) (gitx.MirrorVerification, error)
	ProjectReady  func(context.Context, Request, config.ProjectConfig, model.Project, model.Plan, model.ProjectIdentifiers) error
	SessionReady  func(context.Context, Request) (SessionProof, error)
	JournalWrite  func(string, Request, Receipt) (HubCommittedJournalWriteReceipt, error)
	RecoveryWrite func(string, Request, Receipt) (HubCommittedJournalWriteReceipt, error)
	Now           func() time.Time
}

type ActivationCoordinator struct {
	Hub      hub.Store
	StateDir string
	Git      gitx.Runner
	Airelay  airelay.Client
	Hooks    ActivationHooks
}

func NewActivationCoordinator(store hub.Store) *ActivationCoordinator {
	return &ActivationCoordinator{
		Hub:      store,
		StateDir: store.Config.StateDir,
		Git:      gitx.Runner{MaxReadBytes: store.Config.MaxReadBytes, MaxDiffBytes: store.Config.MaxDiffBytes, MaxListItems: store.Config.MaxListItems},
		Airelay:  airelay.Client{Command: store.Config.AirelayCommand, Timeout: time.Duration(store.Config.DispatchTimeoutSeconds) * time.Second, MaxMessageBytes: airelay.MaxTransportMessageBytes},
	}
}
