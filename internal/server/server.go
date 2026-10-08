package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"aiconnectionmanager/internal/connection"
)

// Config contains the runtime settings for the secure WebSocket service.
type Config struct {
	Address         string
	CertificatePath string
	KeyPath         string
	InstanceID      string
	Interval        time.Duration
	ReportInterval  time.Duration
}

// Run starts the secure WebSocket service until ctx is canceled.
func Run(ctx context.Context, config Config, logger *log.Logger) error {
	if config.Address == "" {
		return fmt.Errorf("server address is required")
	}
	if config.Interval <= 0 {
		return fmt.Errorf("message interval must be positive")
	}
	if config.ReportInterval <= 0 {
		config.ReportInterval = 30 * time.Second
	}
	if err := connection.EnsureCertificatePair(config.CertificatePath, config.KeyPath); err != nil {
		return err
	}

	certificate, err := tls.LoadX509KeyPair(config.CertificatePath, config.KeyPath)
	if err != nil {
		return fmt.Errorf("load TLS certificate: %w", err)
	}

	listener, err := net.Listen("tcp", config.Address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", config.Address, err)
	}
	registry := connection.NewConnectionRegistry(config.InstanceID)
	instanceID := config.InstanceID
	if instanceID == "" {
		instanceID = "instance"
	}
	tlsListener := tls.NewListener(listener, &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS13,
	})

	server := &http.Server{
		Handler:           connection.NewWebSocketHandler(config.Interval, registry),
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	serverContext, cancelServer := context.WithCancel(ctx)
	defer cancelServer()
	connection.StartConnectionReporter(serverContext, registry, logger, config.ReportInterval)
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(tlsListener)
	}()

	logger.Printf("secure WebSocket service listening on wss://%s%s instanceID=%s", listener.Addr(), connection.WebSocketPath, instanceID)

	select {
	case err := <-serverErrors:
		registry.CloseAll()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve websocket service: %w", err)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			registry.CloseAll()
			return fmt.Errorf("shutdown websocket service: %w", err)
		}
		registry.CloseAll()
		return nil
	}
}
