package mcp

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestOperationReadIsSessionBoundAndLocalOnly(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "operation-test", StateDir: t.TempDir()}), AuthorityContext: authority.WithDelivery(context.Background())}
	entries := server.genericActionRegistry(server.tools())
	entry, ok := entries["operation/read"]
	if !ok {
		t.Fatal("operation/read is not registered")
	}
	if !entry.SessionBound || !entry.SessionRequired || !entry.LocalReceiptOnly {
		t.Fatalf("operation/read authority=%#v", entry.GenericAction)
	}
	if _, ok := entry.ExecutionInputSchema["properties"].(map[string]any); !ok {
		t.Fatal("operation/read has no execution schema")
	}
}
