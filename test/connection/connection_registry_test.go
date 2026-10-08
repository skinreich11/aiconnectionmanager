package connection_test

import (
	"bytes"
	"context"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	connectionpkg "aiconnectionmanager/internal/connection"
	"github.com/gorilla/websocket"
)

func TestConnectionRegistryTracksMetadataAndCleansUpConnections(t *testing.T) {
	registry := connectionpkg.NewConnectionRegistry("node-a")
	firstConnection := &websocket.Conn{}
	secondConnection := &websocket.Conn{}

	first, err := registry.Register("client-a", "127.0.0.1:49152", firstConnection)
	if err != nil {
		t.Fatalf("register first connection: %v", err)
	}
	if first.ClientID != "client-a" || first.Endpoint != "127.0.0.1" || first.Port != "49152" || first.WebSocket != firstConnection {
		t.Fatalf("first connection metadata = %+v", first)
	}

	second, err := registry.Register("", "[::1]:60000", secondConnection)
	if err != nil {
		t.Fatalf("register generated connection: %v", err)
	}
	if second.ClientID != "node-a-1" || second.Endpoint != "::1" || second.Port != "60000" {
		t.Fatalf("generated connection metadata = %+v", second)
	}

	if _, err := registry.Register("client-a", "127.0.0.1:49153", &websocket.Conn{}); err == nil {
		t.Fatal("duplicate client ID should be rejected")
	}
	if _, err := registry.Register("client with spaces", "127.0.0.1:49154", &websocket.Conn{}); err == nil {
		t.Fatal("invalid client ID should be rejected")
	}

	snapshot := registry.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("snapshot length = %d, want 2", len(snapshot))
	}
	if registry.Unregister("client-a", &websocket.Conn{}) {
		t.Fatal("unregister should not remove a different connection with the same client ID")
	}
	if !registry.Unregister("client-a", firstConnection) {
		t.Fatal("unregister should remove the matching connection")
	}
	if len(registry.Snapshot()) != 1 {
		t.Fatalf("snapshot length after unregister = %d, want 1", len(registry.Snapshot()))
	}
}

func TestConnectionRegistryReportsActiveConnections(t *testing.T) {
	registry := connectionpkg.NewConnectionRegistry("node-a")
	if _, err := registry.Register("client-a", "10.0.0.8:4321", &websocket.Conn{}); err != nil {
		t.Fatalf("register connection: %v", err)
	}

	var output bytes.Buffer
	connectionpkg.ReportActiveConnections(registry, log.New(&output, "", 0))
	report := output.String()
	for _, expected := range []string{
		"active connections: 1",
		"clientID=client-a",
		"endpoint=10.0.0.8",
		"port=4321",
		"websocket=",
	} {
		if !strings.Contains(report, expected) {
			t.Fatalf("report = %q, want substring %q", report, expected)
		}
	}
}

func TestConnectionReporterRunsAndStops(t *testing.T) {
	registry := connectionpkg.NewConnectionRegistry("node-a")
	started := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connectionpkg.StartConnectionReporter(ctx, registry, log.New(notificationWriter{notifications: started}, "", 0), time.Millisecond)
	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("connection reporter did not run")
	}
}

func TestConnectionRegistryCloseAllHandlesEmptyRegistry(t *testing.T) {
	registry := connectionpkg.NewConnectionRegistry("node-a")
	registry.CloseAll()
	if len(registry.Snapshot()) != 0 {
		t.Fatalf("closeAll on an empty registry should leave it empty")
	}
}

func TestConnectionRegistryIsSafeForConcurrentRegistrationAndSnapshots(t *testing.T) {
	registry := connectionpkg.NewConnectionRegistry("node-a")
	var waitGroup sync.WaitGroup
	for connectionNumber := 0; connectionNumber < 100; connectionNumber++ {
		waitGroup.Add(1)
		go func(connectionNumber int) {
			defer waitGroup.Done()
			clientID := "client-" + string(rune('a'+connectionNumber%26)) + "-" + string(rune('0'+connectionNumber/26))
			_, _ = registry.Register(clientID, "127.0.0.1:1", &websocket.Conn{})
			_ = registry.Snapshot()
		}(connectionNumber)
	}
	waitGroup.Wait()

	if len(registry.Snapshot()) != 100 {
		t.Fatalf("snapshot length = %d, want 100", len(registry.Snapshot()))
	}
}
