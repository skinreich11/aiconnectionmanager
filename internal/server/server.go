package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aiconnectionmanager/internal/connection"
	"aiconnectionmanager/internal/messaging"
	"aiconnectionmanager/internal/valkey"
)

// Config contains the runtime settings for the secure WebSocket service.
type Config struct {
	Address                     string
	CertificatePath             string
	KeyPath                     string
	InstanceID                  string
	Interval                    time.Duration
	ReportInterval              time.Duration
	Messaging                   messaging.Config
	Valkey                      valkey.Config
	RequireExternalDependencies bool
}

var errDependencyNotConfigured = errors.New("dependency is not configured")

// Run starts the secure WebSocket service until ctx is canceled.
func Run(ctx context.Context, config Config, logger *log.Logger) error {
	if err := validateExternalDependencies(config); err != nil {
		return err
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
	containerID, err := resolveContainerID(serverContext, config)
	if err != nil && !errors.Is(err, errDependencyNotConfigured) {
		return err
	}
	config.Messaging.ContainerID = containerID
	clientContainerStore, err := openValkeyStore(serverContext, config.Valkey)
	if err != nil && !errors.Is(err, errDependencyNotConfigured) {
		return err
	}
	if clientContainerStore != nil {
		defer clientContainerStore.Close()
	}
	subscription, messageErrors, err := startMessageSubscription(serverContext, config.Messaging, logger)
	if err != nil && !errors.Is(err, errDependencyNotConfigured) {
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
		Handler:           connection.NewWebSocketHandlerWithClientContainerStore(config.Interval, registry, clientContainerStore, containerID),
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

func validateExternalDependencies(config Config) error {
	if !config.RequireExternalDependencies {
		return nil
	}
	if config.Messaging.ContainerID != "" {
		return fmt.Errorf("AMQP_CONTAINER_ID must not be set on ECS; use ECS container metadata")
	}
	if strings.TrimSpace(config.Messaging.BrokerURL) == "" {
		return fmt.Errorf("AMQP_URL is required on ECS for Amazon MQ")
	}
	brokerURL, err := url.Parse(config.Messaging.BrokerURL)
	if err != nil || brokerURL.Scheme != "amqps" || brokerURL.Host == "" {
		return fmt.Errorf("AMQP_URL must be an amqps:// Amazon MQ broker URL on ECS")
	}
	if strings.TrimSpace(config.Valkey.URL) == "" {
		return fmt.Errorf("VALKEY_URL is required on ECS for ElastiCache")
	}
	cacheURL, err := url.Parse(config.Valkey.URL)
	if err != nil || cacheURL.Scheme != "rediss" || cacheURL.Host == "" {
		return fmt.Errorf("VALKEY_URL must be a rediss:// ElastiCache endpoint on ECS")
	}
	if config.Address == "" {
		return fmt.Errorf("server address is required")
	}
	if config.Interval <= 0 {
		return fmt.Errorf("message interval must be positive")
	}
	return nil
}

func resolveContainerID(ctx context.Context, config Config) (string, error) {
	if config.Valkey.URL == "" && config.Messaging.BrokerURL == "" {
		return "", errDependencyNotConfigured
	}
	containerID, err := messaging.ResolveContainerID(ctx, config.Messaging)
	if err != nil {
		return "", fmt.Errorf("resolve container ID for RabbitMQ and Valkey: %w", err)
	}
	return containerID, nil
}

func openValkeyStore(ctx context.Context, config valkey.Config) (*valkey.Client, error) {
	if config.URL == "" {
		return nil, errDependencyNotConfigured
	}
	client, err := valkey.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("configure Valkey client: %w", err)
	}
	pingContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingContext); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect to Valkey: %w", err)
	}
	return client, nil
}

func startMessageSubscription(ctx context.Context, config messaging.Config, logger *log.Logger) (*messaging.Subscription, <-chan error, error) {
	if config.BrokerURL == "" {
		return nil, nil, errDependencyNotConfigured
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
