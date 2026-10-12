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
	"aiconnectionmanager/internal/messaging"
)

// Config contains the runtime settings for the secure WebSocket service.
type Config struct {
	Address         string
	CertificatePath string
	KeyPath         string
	InstanceID      string
	Interval        time.Duration
	ReportInterval  time.Duration
	Messaging       messaging.Config
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

	serverContext, cancelServer := context.WithCancel(ctx)
	defer cancelServer()
	subscription, messageErrors, err := startMessageSubscription(serverContext, config.Messaging, logger)
	if err != nil {
		return err
	}
	defer closeSubscription(subscription)

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
	case err := <-messageErrors:
		return handleMessageConsumerStopped(ctx, server, registry, err)
	case <-ctx.Done():
		if err := shutdownServer(ctx, server, registry); err != nil {
			registry.CloseAll()
			return fmt.Errorf("shutdown websocket service: %w", err)
		}
		registry.CloseAll()
		return nil
	}
}

func startMessageSubscription(ctx context.Context, config messaging.Config, logger *log.Logger) (*messaging.Subscription, <-chan error, error) {
	if config.BrokerURL == "" {
		return nil, nil, nil
	}
	consumer, err := messaging.NewConsumer(config, nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("configure RabbitMQ consumer: %w", err)
	}
	subscription, err := consumer.Start(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("start RabbitMQ consumer: %w", err)
	}
	logger.Printf("RabbitMQ consumer subscribed queue=%s", subscription.QueueName())
	messageErrors := make(chan error, 1)
	go func() {
		messageErrors <- subscription.Run(ctx, logger)
	}()
	return subscription, messageErrors, nil
}

func closeSubscription(subscription *messaging.Subscription) {
	if subscription != nil {
		_ = subscription.Close()
	}
}

func shutdownServer(ctx context.Context, server *http.Server, registry *connection.ConnectionRegistry) error {
	shutdownContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	shutdownError := server.Shutdown(shutdownContext)
	registry.CloseAll()
	return shutdownError
}

func handleMessageConsumerStopped(ctx context.Context, server *http.Server, registry *connection.ConnectionRegistry, err error) error {
	if shutdownError := shutdownServer(ctx, server, registry); shutdownError != nil {
		return fmt.Errorf("shutdown websocket service after RabbitMQ failure: %w", shutdownError)
	}
	if err != nil {
		return fmt.Errorf("RabbitMQ consumer stopped: %w", err)
	}
	return fmt.Errorf("RabbitMQ consumer stopped unexpectedly")
}
