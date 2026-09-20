package service

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

type AdminSessionResult struct {
	Session string `json:"session"`
	Status  string `json:"status"`
}

func (s *Service) AdminSessionMint(label *string) (AdminSessionResult, error) {
	if err := requireLinuxAdmin(); err != nil {
		return AdminSessionResult{}, err
	}
	if s.Durability == nil || s.Durability.Local == nil {
		return AdminSessionResult{}, fmt.Errorf("local session store is unavailable")
	}
	record, err := durableSession.NewStoreWithGateway(s.Durability, s.Config.GatewayID).CreateAdmin(label)
	if err != nil {
		return AdminSessionResult{}, err
	}
	s.recordAdminSessionEvent(record, "admin_session_minted")
	return AdminSessionResult{
		Session: record.ID,
		Status:  record.Status,
	}, nil
}

func (s *Service) AdminSessionRevoke(id string) (AdminSessionResult, error) {
	if err := requireLinuxAdmin(); err != nil {
		return AdminSessionResult{}, err
	}
	if s.Durability == nil || s.Durability.Local == nil {
		return AdminSessionResult{}, fmt.Errorf("local session store is unavailable")
	}
	store := durableSession.NewStoreWithGateway(s.Durability, s.Config.GatewayID)
	record, err := store.Get(id)
	if err != nil {
		return AdminSessionResult{}, err
	}
	if record.Role != durableSession.RoleAdmin {
		return AdminSessionResult{}, fmt.Errorf("session is not an Admin Session")
	}
	record, err = store.End(id)
	if err != nil {
		return AdminSessionResult{}, err
	}
	s.recordAdminSessionEvent(record, "admin_session_revoked")
	return AdminSessionResult{
		Session: record.ID,
		Status:  record.Status,
	}, nil
}

func (s *Service) recordAdminSessionEvent(record durableSession.Record, event string) {
	_ = runtime_log.New(s.Config.StateDir).Append(runtime_log.Event{
		Timestamp: time.Now().UTC(),
		Level:     "info",
		Component: "admin",
		Event:     event,
		SessionID: record.ID,
	})
}

func requireLinuxAdmin() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("Admin control plane is Linux-only")
	}
	return nil
}

func (s *Service) AdminSession(ctx context.Context, id string) (durableSession.Record, error) {
	if err := requireLinuxAdmin(); err != nil {
		return durableSession.Record{}, err
	}
	if s.Durability == nil || s.Durability.Local == nil {
		return durableSession.Record{}, fmt.Errorf("local session store is unavailable")
	}
	record, err := durableSession.NewStoreWithGateway(s.Durability, s.Config.GatewayID).Get(id)
	if err != nil {
		return durableSession.Record{}, err
	}
	if record.Role != durableSession.RoleAdmin {
		return durableSession.Record{}, fmt.Errorf("session is not an Admin Session")
	}
	return record, nil
}
