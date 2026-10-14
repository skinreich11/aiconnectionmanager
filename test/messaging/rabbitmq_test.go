package messaging_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	messagingpkg "aiconnectionmanager/internal/messaging"
	"github.com/rabbitmq/amqp091-go"
)

func TestQueueNameUsesContainerID(t *testing.T) {
	queueName, err := messagingpkg.QueueName(
		messagingpkg.DefaultQueuePrefix,
		"container-123",
	)
	if err != nil {
		t.Fatalf("queue name: %v", err)
	}

	if queueName != "prod.connection.aiconnectionmanager.send-request.container-123.q." {
		t.Fatalf("queue name = %q", queueName)
	}
}

func TestQueueNameRejectsUnsafeContainerIDs(t *testing.T) {
	for _, containerID := range []string{"", "container/id", "container id"} {
		t.Run(fmt.Sprintf("id=%q", containerID), func(t *testing.T) {
			if _, err := messagingpkg.QueueName(messagingpkg.DefaultQueuePrefix, containerID); err == nil {
				t.Fatal("QueueName should reject the container ID")
			}
		})
	}
}

func TestContainerMetadataResolverReturnsCurrentContainerID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			t.Fatalf("metadata path = %q, want %q", request.URL.Path, "/")
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"DockerId":"docker-container-123"}`))
	}))
	defer server.Close()

	resolver := messagingpkg.NewContainerMetadataResolver(server.URL, server.Client())
	containerID, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("resolve container ID: %v", err)
	}
	if containerID != "docker-container-123" {
		t.Fatalf("container ID = %q", containerID)
	}
}

func TestResolveContainerIDUsesSameOverrideAsQueueConfiguration(t *testing.T) {
	containerID, err := messagingpkg.ResolveContainerID(context.Background(), messagingpkg.Config{
		ContainerID: "container-123",
	})
	if err != nil {
		t.Fatalf("resolve container ID: %v", err)
	}
	if containerID != "container-123" {
		t.Fatalf("container ID = %q, want %q", containerID, "container-123")
	}
}

func TestContainerMetadataResolverRejectsHTTPFailuresAndInvalidPayloads(t *testing.T) {
	tests := []struct {
		name    string
		handler http.Handler
		want    string
	}{
		{
			name: "status failure",
			handler: http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
				http.Error(responseWriter, "unavailable", http.StatusServiceUnavailable)
			}),
			want: "status 503",
		},
		{
			name: "missing docker id",
			handler: http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
				_, _ = responseWriter.Write([]byte(`{"DockerId":""}`))
			}),
			want: "DockerId",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			resolver := messagingpkg.NewContainerMetadataResolver(server.URL, server.Client())

			_, err := resolver.Resolve(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestConsumerDeclaresContainerQueueAndLogsAcknowledgedEvents(t *testing.T) {
	deliveries := make(chan amqp091.Delivery, 1)
	channel := &fakeChannel{deliveries: deliveries}
	connection := &fakeConnection{channel: channel}
	resolver := fakeResolver{containerID: "container-456"}

	consumer, err := messagingpkg.NewConsumer(messagingpkg.Config{
		BrokerURL:   "amqp://localhost:5672",
		Username:    "user",
		Password:    "password",
		QueuePrefix: messagingpkg.DefaultQueuePrefix,
	}, resolver, func(string, amqp091.Config) (messagingpkg.Connection, error) {
		return connection, nil
	})
	if err != nil {
		t.Fatalf("new consumer: %v", err)
	}

	subscription, err := consumer.Start(context.Background())
	if err != nil {
		t.Fatalf("start consumer: %v", err)
	}
	defer subscription.Close()

	wantQueue := "prod.connection.aiconnectionmanager.send-request.container-456.q."
	if channel.declaredQueue != wantQueue || channel.consumedQueue != wantQueue {
		t.Fatalf("declared queue = %q, consumed queue = %q, want %q", channel.declaredQueue, channel.consumedQueue, wantQueue)
	}
	if channel.autoAck {
		t.Fatal("consumer must use manual acknowledgements")
	}

	acknowledger := &fakeAcknowledger{}
	deliveries <- amqp091.Delivery{Body: []byte(`{"request":"hello"}`), DeliveryTag: 7, Acknowledger: acknowledger}

	var output bytes.Buffer
	consumeContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	consumeResult := make(chan error, 1)
	go func() {
		consumeResult <- subscription.Run(consumeContext, log.New(&output, "", 0))
	}()

	if !waitFor(t, time.Second, func() bool { return acknowledger.ackCount() == 1 }) {
		t.Fatal("event was not acknowledged")
	}
	cancel()
	select {
	case err := <-consumeResult:
		if err != nil {
			t.Fatalf("consume event: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("consumer did not stop after context cancellation")
	}
	if !strings.Contains(output.String(), "event received: {\"request\":\"hello\"}") {
		t.Fatalf("log output = %q", output.String())
	}
}

func TestConsumerRequiresCredentialsAndContainerIdentity(t *testing.T) {
	tests := []struct {
		name     string
		config   messagingpkg.Config
		resolver fakeResolver
		want     string
	}{
		{
			name: "missing broker URL",
			config: messagingpkg.Config{
				Username: "user",
				Password: "password",
			},
			resolver: fakeResolver{containerID: "container-123"},
			want:     "broker URL",
		},
		{
			name: "missing username",
			config: messagingpkg.Config{
				BrokerURL: "amqp://localhost:5672",
				Password:  "password",
			},
			resolver: fakeResolver{containerID: "container-123"},
			want:     "username",
		},
		{
			name: "missing container ID",
			config: messagingpkg.Config{
				BrokerURL: "amqp://localhost:5672",
				Username:  "user",
				Password:  "password",
			},
			resolver: fakeResolver{},
			want:     "container ID",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			consumer, err := messagingpkg.NewConsumer(test.config, test.resolver, nil)
			if err == nil {
				_, err = consumer.Start(context.Background())
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestConsumerRejectsCredentialsEmbeddedInBrokerURL(t *testing.T) {
	_, err := messagingpkg.NewConsumer(messagingpkg.Config{
		BrokerURL: "amqps://user:password@broker.example:5671",
		Username:  "user",
		Password:  "password",
	}, fakeResolver{containerID: "container-123"}, nil)
	if err == nil || !strings.Contains(err.Error(), "must not contain credentials") {
		t.Fatalf("error = %v, want embedded credential rejection", err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return condition()
}

type fakeResolver struct {
	containerID string
}

func (resolver fakeResolver) Resolve(context.Context) (string, error) {
	if resolver.containerID == "" {
		return "", errors.New("container ID unavailable")
	}
	return resolver.containerID, nil
}

type fakeConnection struct {
	channel *fakeChannel
}

func (connection *fakeConnection) Channel() (messagingpkg.Channel, error) {
	return connection.channel, nil
}

func (connection *fakeConnection) Close() error {
	return nil
}

type fakeChannel struct {
	deliveries    <-chan amqp091.Delivery
	declaredQueue string
	consumedQueue string
	autoAck       bool
	closeMutex    sync.Mutex
	closed        bool
}

func (channel *fakeChannel) QueueDeclare(name string, _, _, _, _ bool, _ amqp091.Table) (amqp091.Queue, error) {
	channel.declaredQueue = name
	return amqp091.Queue{Name: name}, nil
}

func (channel *fakeChannel) Consume(name, _ string, autoAck, _, _, _ bool, _ amqp091.Table) (<-chan amqp091.Delivery, error) {
	channel.consumedQueue = name
	channel.autoAck = autoAck
	return channel.deliveries, nil
}

func (channel *fakeChannel) Close() error {
	channel.closeMutex.Lock()
	defer channel.closeMutex.Unlock()
	channel.closed = true
	return nil
}

type fakeAcknowledger struct {
	mutex sync.Mutex
	acks  int
}

func (acknowledger *fakeAcknowledger) Ack(uint64, bool) error {
	acknowledger.mutex.Lock()
	defer acknowledger.mutex.Unlock()
	acknowledger.acks++
	return nil
}

func (acknowledger *fakeAcknowledger) Nack(uint64, bool, bool) error { return nil }

func (acknowledger *fakeAcknowledger) Reject(uint64, bool) error { return nil }

func (acknowledger *fakeAcknowledger) ackCount() int {
	acknowledger.mutex.Lock()
	defer acknowledger.mutex.Unlock()
	return acknowledger.acks
}
