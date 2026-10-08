package connection

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

const (
	GreetingMessage = "Hello World!"
	WebSocketPath   = "/ws"
	ClientIDHeader  = "X-Client-ID"
)

func NewWebSocketHandler(interval time.Duration, registry *ConnectionRegistry) http.Handler {
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

		serveWebSocketConnection(connection, interval, registry, info.ClientID)
	})
}

func serveWebSocketConnection(connection *websocket.Conn, interval time.Duration, registry *ConnectionRegistry, clientID string) {
	defer connection.Close()
	defer registry.Unregister(clientID, connection)
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

func validateWebSocketURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("websocket URL is required")
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse websocket URL: %w", err)
	}
	if parsedURL.Scheme != "wss" {
		return fmt.Errorf("websocket URL must use wss scheme")
	}
	if parsedURL.Hostname() == "" {
		return fmt.Errorf("websocket URL must include a host")
	}
	return nil
}
