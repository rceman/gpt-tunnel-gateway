package application

import (
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/persistence"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

// Runtime is the local application composition owned by gatewayd. It owns
// exactly one Shared database and one typed evidence store for the daemon
// lifetime.
type Runtime struct {
	Service    *service.Service
	Durability *sqlitestore.Databases
}

// OpenRuntime opens local durable authority and wires the service dependencies
// used by gatewayd. Background workers remain deferred until HTTP readiness.
func OpenRuntime(c config.Config, observe func(string)) (*Runtime, error) {
	phase := func(name string) {
		if observe != nil {
			observe(name)
		}
	}
	phase("SQLITE_OPEN")
	durability, err := sqlitestore.OpenWithObserver(c.StateDir, phase)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{
		Durability: durability,
		Service: service.NewWithDurabilityDeferredWorkersAndEvidence(
			c,
			durability,
			persistence.NewLocalEvidenceStore(c.StateDir),
		),
	}
	phase("LOCAL_STATE_READY")
	return runtime, nil
}

func (r *Runtime) Close() error {
	if r == nil || r.Durability == nil {
		return nil
	}
	return r.Durability.Close()
}
