package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/callbackdelivery"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

const callbackDeliveryTimeout = 5 * time.Second

func (s *Service) startCallbackWorker() {
	if s.Durability == nil {
		return
	}
	s.callbackWorkerOnce.Do(func() { go s.callbackWorker() })
}

func (s *Service) callbackWorker() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = s.processCallbackEpochs(ctx)
		cancel()
	}
}

func (s *Service) processCallbackEpochs(ctx context.Context) error {
	if s.Durability == nil {
		return fmt.Errorf("Shared callback epoch store is unavailable")
	}
	epochs, err := s.Durability.PendingCallbackEpochs(ctx, 32)
	if err != nil {
		return err
	}
	for _, epoch := range epochs {
		if err := s.processCallbackEpoch(ctx, epoch); err != nil {
			s.recordCallbackDiagnostic(epoch, err)
		}
	}
	return nil
}

func (s *Service) processCallbackEpoch(ctx context.Context, epoch sqlitestore.CallbackEpoch) error {
	statusCtx, cancel := context.WithTimeout(ctx, time.Second)
	status, err := s.Airelay.Status(statusCtx, epoch.SessionKey)
	cancel()
	if err != nil {
		_, observeErr := s.Durability.ObserveCallbackEpoch(ctx, epoch.ID, "error")
		return errors.Join(err, observeErr)
	}
	ready, err := s.Durability.ObserveCallbackEpoch(ctx, epoch.ID, status.State)
	if err != nil || !ready {
		return err
	}
	configuration, err := s.readCallbackConfiguration(ctx, epoch.ProjectID)
	if err != nil {
		return err
	}
	payload, err := callbackEventPayload(epoch.ID, epoch.ProjectID, epoch.AgentID)
	if err != nil {
		return err
	}
	claimed, err := s.Durability.ClaimCallbackEpoch(ctx, epoch.ID, s.durableNow())
	if err != nil || !claimed {
		return err
	}
	return s.deliverCallbackSet(ctx, epoch.ProjectID, configuration.Callbacks, payload)
}

func (s *Service) deliverCallbackSet(ctx context.Context, projectID string, callbacks []model.ProjectCallback, payload []byte) error {
	project, projectErr := s.EffectiveProjectConfig(projectID)
	var wg sync.WaitGroup
	errorsCh := make(chan error, len(callbacks))
	for _, callback := range callbacks {
		if callback.Event != model.ProjectCallbackWorkFinishedEvent {
			continue
		}
		callback := callback
		wg.Add(1)
		go func() {
			defer wg.Done()
			if callback.Script != nil && projectErr != nil {
				errorsCh <- fmt.Errorf("callback %q project checkout unavailable: %w", callback.Callback, projectErr)
				return
			}
			deliveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), callbackDeliveryTimeout)
			err := callbackdelivery.Deliver(deliveryCtx, callback, project, s.Git, payload)
			cancel()
			if err != nil {
				errorsCh <- err
			}
		}()
	}
	wg.Wait()
	close(errorsCh)
	var deliveryErr error
	for err := range errorsCh {
		deliveryErr = errors.Join(deliveryErr, err)
	}
	return deliveryErr
}

func (s *Service) recordCallbackDiagnostic(epoch sqlitestore.CallbackEpoch, err error) {
	if s.Config.StateDir == "" || err == nil {
		return
	}
	_ = runtime_log.New(s.Config.StateDir).Append(runtime_log.Event{Timestamp: s.durableNow(), Level: "warn", Component: "callback", Event: "callback_delivery_failure", OperationID: epoch.ID, ProjectID: epoch.ProjectID, Message: "Agent work-finished callback delivery failed", Error: runtime_log.SanitizeText(err.Error())})
}

func (s *Service) armAgentWorkFinished(ctx context.Context, projectID, agentID, sessionKey string) {
	if s.Durability == nil || projectID == "" || sessionKey == "" {
		return
	}
	epochID, err := model.NewID()
	if err != nil {
		return
	}
	epochID = "agent-work-" + epochID
	if err := s.Durability.ArmCallbackEpoch(ctx, sqlitestore.CallbackEpoch{ID: epochID, ProjectID: projectID, AgentID: agentID, SessionKey: sessionKey, ArmedAt: s.durableNow()}); err != nil {
		s.recordCallbackDiagnostic(sqlitestore.CallbackEpoch{ID: epochID, ProjectID: projectID}, err)
	}
}
