package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/mcp"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

var version = "0.2.1"

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
	srv := &http.Server{Addr: c.ListenAddr, Handler: (&mcp.Server{Service: svc}).Router(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 32 << 10}
	fmt.Fprintf(os.Stderr, "gpt-tunnel-gatewayd %s listening on %s\n", version, c.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal(err)
	}
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "gpt-tunnel-gatewayd:", err); os.Exit(1) }
