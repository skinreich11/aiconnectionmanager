package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ContainerIDResolver obtains the ID used to isolate a task's request queue.
type ContainerIDResolver interface {
	Resolve(context.Context) (string, error)
}

// ResolveContainerID resolves the identity shared by RabbitMQ queues and Valkey mappings.
func ResolveContainerID(ctx context.Context, config Config) (string, error) {
	if config.ContainerID != "" {
		if _, err := QueueName(DefaultQueuePrefix, config.ContainerID); err != nil {
			return "", err
		}
		return config.ContainerID, nil
	}
	return NewContainerMetadataResolver(config.MetadataURL, nil).Resolve(ctx)
}

type staticContainerIDResolver string

func (resolver staticContainerIDResolver) Resolve(context.Context) (string, error) {
	return string(resolver), nil
}

// ContainerMetadataResolver resolves the current ECS container from the v4 metadata endpoint.
type ContainerMetadataResolver struct {
	baseURL string
	client  *http.Client
}

// NewContainerMetadataResolver creates an ECS metadata resolver.
func NewContainerMetadataResolver(baseURL string, client *http.Client) *ContainerMetadataResolver {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	return &ContainerMetadataResolver{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (resolver *ContainerMetadataResolver) Resolve(ctx context.Context) (string, error) {
	if resolver.baseURL == "" {
		return "", fmt.Errorf("ECS container metadata URL is required")
	}
	parsedURL, err := url.Parse(resolver.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse ECS container metadata URL: %w", err)
	}
	if (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return "", fmt.Errorf("ECS container metadata URL must include an HTTP(S) host")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resolver.baseURL+"/", nil)
	if err != nil {
		return "", fmt.Errorf("create ECS metadata request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := resolver.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("request ECS container metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("ECS container metadata returned status %d", response.StatusCode)
	}

	var metadata struct {
		DockerID string `json:"DockerId"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&metadata); err != nil {
		return "", fmt.Errorf("decode ECS container metadata: %w", err)
	}
	if _, err := QueueName(DefaultQueuePrefix, metadata.DockerID); err != nil {
		return "", fmt.Errorf("ECS container metadata DockerId is invalid: %w", err)
	}
	return metadata.DockerID, nil
}
