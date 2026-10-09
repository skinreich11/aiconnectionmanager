package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"aiconnectionmanager/internal/connection"
	"github.com/gorilla/websocket"
)

// Run connects to the secure WebSocket service and writes received text messages to output.
func Run(ctx context.Context, rawURL, certificatePath string, output io.Writer) error {
	return RunWithID(ctx, rawURL, certificatePath, "", output)
}

// RunWithID connects to the secure WebSocket service with an optional client ID.
func RunWithID(ctx context.Context, rawURL, certificatePath, clientID string, output io.Writer) error {
	if err := validateWebSocketURL(rawURL); err != nil {
		return err
	}
	if err := connection.ValidateClientID(clientID); err != nil {
		return err
	}
	rootCAs, err := LoadRootCAs(certificatePath)
	if err != nil {
		return err
	}

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			RootCAs:    rootCAs,
		},
	}
	headers := http.Header{}
	if clientID != "" {
		headers.Set(connection.ClientIDHeader, clientID)
	}
	//nolint:bodyclose // gorilla/websocket documents that handshake bodies do not need closing.
	connection, _, err := dialer.DialContext(ctx, rawURL, headers)
	if err != nil {
		return fmt.Errorf("connect to websocket service: %w", err)
	}
	defer connection.Close()

	connectionClosed := make(chan struct{})
	defer close(connectionClosed)
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-connectionClosed:
		}
	}()

	for {
		messageType, message, err := connection.ReadMessage()
		if err != nil {
			if ctx.Err() != nil || websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return fmt.Errorf("read websocket message: %w", err)
		}
		if messageType != websocket.TextMessage {
			continue
		}
		if _, err := fmt.Fprintln(output, string(message)); err != nil {
			return fmt.Errorf("write websocket message: %w", err)
		}
	}
}

// LoadRootCAs loads the certificate authority used to verify the WebSocket service.
func LoadRootCAs(certificatePath string) (*x509.CertPool, error) {
	// #nosec G304 -- the CA path is explicit local client configuration.
	certificatePEM, err := os.ReadFile(certificatePath)
	if err != nil {
		return nil, fmt.Errorf("read CA certificate %q: %w", certificatePath, err)
	}

	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(certificatePEM) {
		return nil, fmt.Errorf("CA certificate %q is not valid PEM", certificatePath)
	}
	return rootCAs, nil
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
