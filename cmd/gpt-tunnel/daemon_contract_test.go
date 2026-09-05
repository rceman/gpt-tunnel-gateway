package main

import (
	"os"
	"strings"
	"testing"
)

func TestPublicDaemonSurfaceIsExactlyFourOperations(t *testing.T) {
	data, err := os.ReadFile("daemon_commands.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, operation := range []string{"install", "status", "restart", "remove"} {
		if !strings.Contains(source, "case \""+operation+"\":") {
			t.Fatalf("public daemon operation %q is not dispatched", operation)
		}
	}
	if !strings.Contains(source, "usage: gpt-tunnel daemon {install|status|restart|remove}") {
		t.Fatal("daemon usage does not define the canonical four-operation surface")
	}
	if strings.Contains(source, "case \"start\":") || strings.Contains(source, "case \"stop\":") {
		t.Fatal("daemon surface contains an obsolete lifecycle operation")
	}
}
