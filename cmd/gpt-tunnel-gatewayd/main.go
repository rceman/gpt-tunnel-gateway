package main

import (
	"context"
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
	durability, err := sqlitestore.Open(c.StateDir)
	if err != nil {
		fatal(err)
	}
	defer durability.Close()
	svc := service.NewWithDurability(c, durability)
	startupPhase("HUB_ENSURE")
	hubCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	if err := svc.Hub.Ensure(hubCtx); err != nil {
		cancel()
		fatal(err)
	}
	startupPhase("STATE_CHECK")
	state, err := svc.StateCheck(hubCtx)
	if err != nil {
		cancel()
		fatal(err)
	}
	if !state.Valid {
		cancel()
		fatal(fmt.Errorf("durable state validation failed: %s", summarizeStateIssues(state.Issues)))
	}
	cancel()
	// Keep the legacy typed-tool authority exact. session.start performs a
	// checked, narrow bootstrap elevation for either durable role; all other
	// handlers retain the daemon's established delivery root.
	trustedMCPContext := authority.WithDelivery(context.Background())
	go svc.RunWatcherSupervisors(context.Background())
	srv := newGatewayHTTPServer(c.ListenAddr, (&mcp.Server{Service: svc, AuthorityContext: trustedMCPContext}).Router())
	startupPhase("HTTP_LISTEN")
	listener, err := net.Listen("tcp", c.ListenAddr)
	if err != nil {
		startupPhase("HTTP_LISTEN_FAILED")
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "gpt-tunnel-gatewayd %s listening on %s\n", version, c.ListenAddr)
	startupPhase("HTTP_READY")
	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		fatal(err)
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

func summarizeStateIssues(issues []service.StateIssue) string {
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		parts = append(parts, issue.Code+": "+issue.Detail)
	}
	return strings.Join(parts, "; ")
}
