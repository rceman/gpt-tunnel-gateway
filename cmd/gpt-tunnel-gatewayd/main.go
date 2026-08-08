package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/mcp"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

var version = "0.6.8"

func main() {
	configPath := flag.String("config", config.DefaultPath(), "configuration file")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	c, err := config.Load(*configPath)
	if err != nil {
		fatal(err)
	}
	svc := service.New(c)
	hubCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	if err := svc.Hub.Ensure(hubCtx); err != nil {
		cancel()
		fatal(err)
	}
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
	trustedMCPContext := authority.WithDelivery(context.Background())
	srv := &http.Server{Addr: c.ListenAddr, Handler: (&mcp.Server{Service: svc, AuthorityContext: trustedMCPContext}).Router(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 32 << 10}
	fmt.Fprintf(os.Stderr, "gpt-tunnel-gatewayd %s listening on %s\n", version, c.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal(err)
	}
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "gpt-tunnel-gatewayd:", err); os.Exit(1) }

func summarizeStateIssues(issues []service.StateIssue) string {
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		parts = append(parts, issue.Code+": "+issue.Detail)
	}
	return strings.Join(parts, "; ")
}
