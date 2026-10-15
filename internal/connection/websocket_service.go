package connection

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	GreetingMessage = "Hello World!"
	WebSocketPath   = "/ws"
	ClientIDHeader  = "X-Client-ID"
)

// ClientContainerStore persists the container serving a connected client.
type ClientContainerStore interface {
	Set(context.Context, string, string) error
	Delete(context.Context, string) error
}

// NewWebSocketHandler creates a WebSocket handler without external client mapping.
func NewWebSocketHandler(interval time.Duration, registry *ConnectionRegistry) http.Handler {
	return NewWebSocketHandlerWithClientContainerStore(interval, registry, nil, "")
}

// NewWebSocketHandlerWithClientContainerStore creates a WebSocket handler that persists client mappings.
func NewWebSocketHandlerWithClientContainerStore(interval time.Duration, registry *ConnectionRegistry, clientContainerStore ClientContainerStore, containerID string) http.Handler {
	if interval <= 0 {
		interval = time.Minute
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != WebSocketPath {
			http.NotFound(responseWriter, request)
			return
		}
		requestedClientID := request.Header.Get(ClientIDHeader)
		if err := ValidateClientID(requestedClientID); err != nil {
			http.Error(responseWriter, err.Error(), http.StatusBadRequest)
			return
		}

		connection, err := upgrader.Upgrade(responseWriter, request, nil)
		if err != nil {
			return
		}

		info, err := registry.Register(requestedClientID, request.RemoteAddr, connection)
		if err != nil {
			_ = connection.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, err.Error()),
				time.Now().Add(time.Second),
			)
			_ = connection.Close()
			return
		}

		if clientContainerStore != nil {
			if err := clientContainerStore.Set(request.Context(), info.ClientID, containerID); err != nil {
				_ = registry.Unregister(info.ClientID, connection)
				_ = connection.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "connection registration failed"),
					time.Now().Add(time.Second),
				)
				_ = connection.Close()
				return
			}
		}

		serveWebSocketConnection(request.Context(), connection, interval, registry, info.ClientID, clientContainerStore)
	})
}

func serveWebSocketConnection(ctx context.Context, connection *websocket.Conn, interval time.Duration, registry *ConnectionRegistry, clientID string, clientContainerStore ClientContainerStore) {
	defer connection.Close()
	defer func() {
		if clientContainerStore != nil {
			cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			_ = clientContainerStore.Delete(cleanupContext, clientID)
			cancel()
		}
		registry.Unregister(clientID, connection)
	}()
	connection.SetReadLimit(64 * 1024)

	readErrors := make(chan error, 1)
	go func() {
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				readErrors <- err
				return
			}
		}
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := connection.WriteMessage(websocket.TextMessage, []byte(GreetingMessage)); err != nil {
				return
			}
		case <-readErrors:
			return
		}
	}
}
