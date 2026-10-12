package messaging

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rabbitmq/amqp091-go"
)

// Config contains the RabbitMQ connection and queue settings.
type Config struct {
	BrokerURL   string
	Username    string
	Password    string
	QueuePrefix string
	MetadataURL string
	ContainerID string
}

// Connection is the subset of an AMQP connection used by the consumer.
type Connection interface {
	Channel() (Channel, error)
	Close() error
}

// Channel is the subset of an AMQP channel used by the consumer.
type Channel interface {
	QueueDeclare(string, bool, bool, bool, bool, amqp091.Table) (amqp091.Queue, error)
	Consume(string, string, bool, bool, bool, bool, amqp091.Table) (<-chan amqp091.Delivery, error)
	Close() error
}

// Dialer creates an AMQP connection. It is injectable so startup and consumer behavior can be tested without a broker.
type Dialer func(string, amqp091.Config) (Connection, error)

// Consumer creates a task-specific queue and subscribes to its events.
type Consumer struct {
	config   Config
	resolver ContainerIDResolver
	dialer   Dialer
}

// NewConsumer validates configuration and creates a RabbitMQ consumer.
func NewConsumer(config Config, resolver ContainerIDResolver, dialer Dialer) (*Consumer, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if resolver == nil {
		if config.ContainerID != "" {
			resolver = staticContainerIDResolver(config.ContainerID)
		} else {
			resolver = NewContainerMetadataResolver(config.MetadataURL, nil)
		}
	}
	if dialer == nil {
		dialer = dialAMQP
	}
	return &Consumer{config: config, resolver: resolver, dialer: dialer}, nil
}

// Validate verifies the required broker settings without exposing the password in errors or logs.
func (config Config) Validate() error {
	if strings.TrimSpace(config.BrokerURL) == "" {
		return fmt.Errorf("RabbitMQ broker URL is required")
	}
	parsedURL, err := url.Parse(config.BrokerURL)
	if err != nil {
		return fmt.Errorf("parse RabbitMQ broker URL: %w", err)
	}
	if parsedURL.User != nil {
		return fmt.Errorf("RabbitMQ broker URL must not contain credentials; use AMQP_USERNAME and AMQP_PASSWORD")
	}
	if (parsedURL.Scheme != "amqp" && parsedURL.Scheme != "amqps") || parsedURL.Host == "" {
		return fmt.Errorf("RabbitMQ broker URL must use amqp or amqps and include a host")
	}
	if strings.TrimSpace(config.Username) == "" {
		return fmt.Errorf("RabbitMQ username is required")
	}
	if config.Password == "" {
		return fmt.Errorf("RabbitMQ password is required")
	}
	return nil
}

// Start declares the per-container queue and begins a manual-acknowledgement subscription.
func (consumer *Consumer) Start(ctx context.Context) (*Subscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	containerID, err := consumer.resolver.Resolve(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve ECS container ID: %w", err)
	}
	queueName, err := QueueName(consumer.config.QueuePrefix, containerID)
	if err != nil {
		return nil, err
	}

	connection, err := consumer.dialer(consumer.config.BrokerURL, amqp091.Config{
		SASL:      []amqp091.Authentication{&amqp091.PlainAuth{Username: consumer.config.Username, Password: consumer.config.Password}},
		Heartbeat: 10 * time.Second,
		Locale:    "en_US",
	})
	if err != nil {
		return nil, fmt.Errorf("connect to RabbitMQ: %w", err)
	}
	channel, err := connection.Channel()
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("open RabbitMQ channel: %w", err)
	}
	if _, err := channel.QueueDeclare(queueName, false, true, true, false, nil); err != nil {
		_ = channel.Close()
		_ = connection.Close()
		return nil, fmt.Errorf("declare RabbitMQ queue %q: %w", queueName, err)
	}
	deliveries, err := channel.Consume(queueName, "aiconnectionmanager", false, true, false, false, nil)
	if err != nil {
		_ = channel.Close()
		_ = connection.Close()
		return nil, fmt.Errorf("subscribe to RabbitMQ queue %q: %w", queueName, err)
	}
	return &Subscription{
		connection: connection,
		channel:    channel,
		queueName:  queueName,
		deliveries: deliveries,
	}, nil
}

func dialAMQP(brokerURL string, config amqp091.Config) (Connection, error) {
	connection, err := amqp091.DialConfig(brokerURL, config)
	if err != nil {
		return nil, err
	}
	return &amqpConnection{connection: connection}, nil
}

type amqpConnection struct {
	connection *amqp091.Connection
}

func (connection *amqpConnection) Channel() (Channel, error) {
	channel, err := connection.connection.Channel()
	if err != nil {
		return nil, err
	}
	return &amqpChannel{channel: channel}, nil
}

func (connection *amqpConnection) Close() error {
	return connection.connection.Close()
}

type amqpChannel struct {
	channel *amqp091.Channel
}

func (channel *amqpChannel) QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, arguments amqp091.Table) (amqp091.Queue, error) {
	return channel.channel.QueueDeclare(name, durable, autoDelete, exclusive, noWait, arguments)
}

func (channel *amqpChannel) Consume(name, consumer string, autoAck, exclusive, noLocal, noWait bool, arguments amqp091.Table) (<-chan amqp091.Delivery, error) {
	return channel.channel.Consume(name, consumer, autoAck, exclusive, noLocal, noWait, arguments)
}

func (channel *amqpChannel) Close() error {
	return channel.channel.Close()
}

// Subscription owns the AMQP resources for one task-specific queue.
type Subscription struct {
	connection Connection
	channel    Channel
	queueName  string
	deliveries <-chan amqp091.Delivery
	closeOnce  sync.Once
	closeError error
}

// QueueName returns the queue assigned to this subscription.
func (subscription *Subscription) QueueName() string {
	return subscription.queueName
}

// Run logs each received message and acknowledges it after logging.
func (subscription *Subscription) Run(ctx context.Context, logger *log.Logger) error {
	if logger == nil {
		return fmt.Errorf("message logger is required")
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case delivery, ok := <-subscription.deliveries:
			if !ok {
				return fmt.Errorf("RabbitMQ delivery channel closed")
			}
			logger.Printf("event received: %s", string(delivery.Body))
			if err := delivery.Ack(false); err != nil {
				return fmt.Errorf("acknowledge RabbitMQ event: %w", err)
			}
		}
	}
}

// Close releases the channel and connection. It is safe to call more than once.
func (subscription *Subscription) Close() error {
	subscription.closeOnce.Do(func() {
		channelError := subscription.channel.Close()
		connectionError := subscription.connection.Close()
		switch {
		case channelError != nil && connectionError != nil:
			subscription.closeError = fmt.Errorf("close RabbitMQ channel: %w; close connection: %w", channelError, connectionError)
		case channelError != nil:
			subscription.closeError = fmt.Errorf("close RabbitMQ channel: %w", channelError)
		case connectionError != nil:
			subscription.closeError = fmt.Errorf("close RabbitMQ connection: %w", connectionError)
		}
	})
	return subscription.closeError
}
