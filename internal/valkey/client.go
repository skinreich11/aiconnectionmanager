package valkey

import (
	"context"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Config contains the connection settings for Valkey or Redis-compatible ElastiCache.
type Config struct {
	URL      string
	Username string
	Password string
}

// Client stores client-to-container mappings in Valkey.
type Client struct {
	client *redis.Client
}

// NewClient creates a Valkey client from a redis:// or rediss:// URL.
func NewClient(config Config) (*Client, error) {
	if strings.TrimSpace(config.URL) == "" {
		return nil, fmt.Errorf("valkey URL is required")
	}

	options, err := redis.ParseURL(config.URL)
	if err != nil {
		return nil, fmt.Errorf("parse valkey URL: %w", err)
	}
	if config.Username != "" {
		options.Username = config.Username
	}
	if config.Password != "" {
		options.Password = config.Password
	}

	return &Client{client: redis.NewClient(options)}, nil
}

// Ping verifies that the configured Valkey instance is reachable.
func (client *Client) Ping(ctx context.Context) error {
	if client == nil || client.client == nil {
		return fmt.Errorf("valkey client is not initialized")
	}
	if err := client.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping valkey: %w", err)
	}
	return nil
}

// Set records the container serving a client. A zero expiration keeps the mapping until it is overwritten.
func (client *Client) Set(ctx context.Context, key, value string) error {
	if client == nil || client.client == nil {
		return fmt.Errorf("valkey client is not initialized")
	}
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("valkey key is required")
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("valkey value is required")
	}
	if err := client.client.Set(ctx, key, value, 0).Err(); err != nil {
		return fmt.Errorf("set valkey key %q: %w", key, err)
	}
	return nil
}

// Delete removes the mapping for a disconnected client.
func (client *Client) Delete(ctx context.Context, key string) error {
	if client == nil || client.client == nil {
		return fmt.Errorf("valkey client is not initialized")
	}
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("valkey key is required")
	}
	if err := client.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("delete valkey key %q: %w", key, err)
	}
	return nil
}

// Close releases the underlying valkey connection pool.
func (client *Client) Close() error {
	if client == nil || client.client == nil {
		return nil
	}
	return client.client.Close()
}
