package connection

import (
	"context"
	"fmt"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const maxClientIDLength = 64

// ConnectionInfo describes one active WebSocket connection.
type ConnectionInfo struct {
	ClientID    string
	Endpoint    string
	Port        string
	WebSocket   *websocket.Conn
	ConnectedAt time.Time
}

// ConnectionRegistry tracks active WebSocket connections for one service instance.
type ConnectionRegistry struct {
	mutex        sync.RWMutex
	instanceID   string
	nextClientID uint64
	connections  map[string]ConnectionInfo
}

func NewConnectionRegistry(instanceID string) *ConnectionRegistry {
	if instanceID == "" {
		instanceID = "instance"
	}
	return &ConnectionRegistry{
		instanceID:  instanceID,
		connections: make(map[string]ConnectionInfo),
	}
}

func (registry *ConnectionRegistry) Register(requestedClientID, remoteAddress string, connection *websocket.Conn) (ConnectionInfo, error) {
	if connection == nil {
		return ConnectionInfo{}, fmt.Errorf("websocket connection is required")
	}
	if err := ValidateClientID(requestedClientID); err != nil {
		return ConnectionInfo{}, err
	}

	registry.mutex.Lock()
	defer registry.mutex.Unlock()

	clientID := requestedClientID
	if clientID == "" {
		registry.nextClientID++
		clientID = fmt.Sprintf("%s-%d", registry.instanceID, registry.nextClientID)
	}
	if _, exists := registry.connections[clientID]; exists {
		return ConnectionInfo{}, fmt.Errorf("client ID %q is already connected", clientID)
	}

	endpoint, port := splitRemoteAddress(remoteAddress)
	info := ConnectionInfo{
		ClientID:    clientID,
		Endpoint:    endpoint,
		Port:        port,
		WebSocket:   connection,
		ConnectedAt: time.Now().UTC(),
	}
	registry.connections[clientID] = info
	return info, nil
}

func (registry *ConnectionRegistry) Unregister(clientID string, connection *websocket.Conn) bool {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()

	info, exists := registry.connections[clientID]
	if !exists || info.WebSocket != connection {
		return false
	}
	delete(registry.connections, clientID)
	return true
}

func (registry *ConnectionRegistry) Snapshot() []ConnectionInfo {
	registry.mutex.RLock()
	connections := make([]ConnectionInfo, 0, len(registry.connections))
	for _, info := range registry.connections {
		connections = append(connections, info)
	}
	registry.mutex.RUnlock()

	sort.Slice(connections, func(first, second int) bool {
		return connections[first].ClientID < connections[second].ClientID
	})
	return connections
}

func (registry *ConnectionRegistry) CloseAll() {
	connections := registry.Snapshot()
	for _, info := range connections {
		if info.WebSocket != nil {
			_ = info.WebSocket.Close()
		}
	}
}

func ValidateClientID(clientID string) error {
	if clientID == "" {
		return nil
	}
	if len(clientID) > maxClientIDLength {
		return fmt.Errorf("client ID must be at most %d characters", maxClientIDLength)
	}
	for _, character := range clientID {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("._-", character) {
			continue
		}
		return fmt.Errorf("client ID may contain only letters, numbers, '.', '_' and '-' characters")
	}
	return nil
}

func splitRemoteAddress(remoteAddress string) (string, string) {
	endpoint, port, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		return remoteAddress, ""
	}
	return endpoint, port
}

func ReportActiveConnections(registry *ConnectionRegistry, logger *log.Logger) {
	connections := registry.Snapshot()
	logger.Printf("active connections: %d", len(connections))
	for _, info := range connections {
		logger.Printf("clientID=%s endpoint=%s port=%s websocket=%p connectedAt=%s", info.ClientID, info.Endpoint, info.Port, info.WebSocket, info.ConnectedAt.Format(time.RFC3339))
	}
}

func StartConnectionReporter(ctx context.Context, registry *ConnectionRegistry, logger *log.Logger, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ReportActiveConnections(registry, logger)
			case <-ctx.Done():
				return
			}
		}
	}()
}
