package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/mcp"
	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

var version = "0.6.11"

func main() {
	configPath := flag.String("config", config.DefaultPath(), "configuration file")
	showVersion := flag.Bool("version", false, "print version")
	showSourceSHA := flag.Bool("source-sha", false, "print the exact source revision embedded by the release builder")
	flag.Parse()
	if *showSourceSHA {
		fmt.Println(releaseartifacts.BuildSourceRevision)
		return
	}
	if *showVersion {
		fmt.Println(version)
		return
	}
	c, err := config.Load(*configPath)
	if err != nil {
		fatal(err)
	}
	runtime, err := bootstrapGateway(c, startupPhase)
	if err != nil {
		startupErrorForPhase("LOCAL_BOOTSTRAP", err)
		fatal(err)
	}
	defer runtime.durability.Close()
	svc := runtime.service
	// HTTP_READY is the local bootstrap boundary. Recovery workers may start
	// only after it; Hub synchronization remains an independent post-ready
	// activity and must not gate listener readiness.
	svc.StartBackgroundWorkers()
	startupPhase("POST_READY_RECOVERY_WORKERS")
	go postReadyHubSync(svc)
	go svc.RunWatcherSupervisors(context.Background())
	if err := <-runtime.serveErr; err != nil && err != http.ErrServerClosed {
		fatal(err)
	}
}

type gatewayRuntime struct {
	service    *service.Service
	durability *sqlitestore.Databases
	server     *http.Server
	listener   net.Listener
	serveErr   chan error
}

func bootstrapGateway(c config.Config, observe func(string)) (*gatewayRuntime, error) {
	startup := func(phase string) {
		if observe != nil {
			observe(phase)
		}
	}
	startup("SQLITE_OPEN")
	durability, err := sqlitestore.OpenWithObserver(c.StateDir, startup)
	if err != nil {
		return nil, err
	}
	svc := service.NewWithDurabilityDeferredWorkers(c, durability)
	startup("LOCAL_STATE_READY")
	// Keep the legacy typed-tool authority exact. session.start performs a
	// checked, narrow bootstrap elevation for either durable role; all other
	// handlers retain the daemon's established delivery root.
	trustedMCPContext := authority.WithDelivery(context.Background())
	srv := newGatewayHTTPServer(c.ListenAddr, (&mcp.Server{Service: svc, AuthorityContext: trustedMCPContext}).Router())
	listener, err := net.Listen("tcp", c.ListenAddr)
	if err != nil {
		_ = durability.Close()
		startup("HTTP_LISTEN_FAILED")
		return nil, err
	}
	startup("HTTP_LISTEN")
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(listener) }()
	fmt.Fprintf(os.Stderr, "gpt-tunnel-gatewayd %s listening on %s\n", version, c.ListenAddr)
	startup("HTTP_READY")
	runtime := &gatewayRuntime{service: svc, durability: durability, server: srv, listener: listener, serveErr: serveErr}
	return runtime, nil
}

func postReadyHubSync(svc *service.Service) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = postReadyHubSyncContext(svc, ctx, startupPhase)
}

func postReadyHubSyncContext(svc *service.Service, ctx context.Context, observe func(string)) error {
	phase := func(name string) {
		if observe != nil {
			observe(name)
		}
	}
	if err := retryPostReadyHubBootstrap(ctx, phase, func(attemptCtx context.Context) error {
		phase("POST_READY_HUB_ENSURE")
		if err := svc.Hub.EnsureWithObserver(attemptCtx, phase); err != nil {
			startupErrorForPhase("POST_READY_HUB_ENSURE", err)
			return err
		}
		phase("POST_READY_SHARED_BOOTSTRAP")
		if err := svc.BootstrapSharedFromHub(attemptCtx); err != nil {
			startupErrorForPhase("POST_READY_SHARED_BOOTSTRAP", err)
			return err
		}
		return nil
	}); err != nil {
		phase("HUB_SYNC_DEGRADED")
		return err
	}
	phase("POST_READY_STATE_CHECK")
	state, err := svc.StateCheck(ctx)
	if err != nil {
		startupErrorForPhase("POST_READY_STATE_CHECK", err)
		phase("HUB_SYNC_DEGRADED")
		return err
	}
	if !state.Valid {
		err = fmt.Errorf("durable state validation failed: %s", summarizeStateIssues(state.Issues))
		startupErrorForPhase("POST_READY_STATE_CHECK", err)
		phase("HUB_SYNC_DEGRADED")
		return err
	}
	phase("HUB_SYNC_READY")
	return nil
}

var postReadyHubRetryDelays = []time.Duration{
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
}

func retryPostReadyHubBootstrap(ctx context.Context, phase func(string), attempt func(context.Context) error) error {
	var lastErr error
	for attemptNumber := 1; ; attemptNumber++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return lastErr
			}
			return err
		}
		if err := attempt(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attemptNumber > len(postReadyHubRetryDelays) {
			return lastErr
		}
		phase(fmt.Sprintf("POST_READY_HUB_RETRY_WAIT_%d", attemptNumber))
		timer := time.NewTimer(postReadyHubRetryDelays[attemptNumber-1])
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return lastErr
		case <-timer.C:
		}
	}
}

func newGatewayHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Long-running actions keep their own bounded command/action contexts;
		// the transport must not impose a shorter fixed response deadline.
		WriteTimeout:   0,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 32 << 10,
	}
}

func fatal(err error) { fmt.Fprintln(os.Stderr, "gpt-tunnel-gatewayd:", err); os.Exit(1) }

func startupPhase(phase string) {
	fmt.Fprintf(os.Stderr, "gpt-tunnel-gatewayd startup_phase=%s\n", phase)
}

func startupError(err error) {
	startupErrorForPhase("SQLITE_OPEN", err)
}

func startupErrorForPhase(phase string, err error) {
	details := map[string]string{
		"phase": phase,
		"stage": "other_initialization",
		"error": boundedStartupError(err),
	}
	var openErr *sqlitestore.OpenError
	if errors.As(err, &openErr) {
		details["stage"] = openErr.Stage
		details["database"] = openErr.Database
		details["path"] = openErr.Path
	}
	payload, marshalErr := json.Marshal(details)
	if marshalErr != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "gpt-tunnel-gatewayd startup_error=%s\n", payload)
}

func boundedStartupError(err error) string {
	const maxErrorBytes = 2048
	message := err.Error()
	if len(message) > maxErrorBytes {
		return message[:maxErrorBytes]
	}
	return message
}

func summarizeStateIssues(issues []service.StateIssue) string {
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		parts = append(parts, issue.Code+": "+issue.Detail)
	}
	return strings.Join(parts, "; ")
}
