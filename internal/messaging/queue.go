package messaging

import (
	"fmt"
	"strings"
)

const (
	DefaultQueuePrefix = "prod.connection.aiconnectionmanager.send-request."
	queueSuffix        = ".q."
	maxQueueNameLength = 255
	maxContainerIDSize = 128
)

// QueueName returns the task-specific request queue name.
func QueueName(prefix, containerID string) (string, error) {
	if strings.TrimSpace(prefix) == "" {
		prefix = DefaultQueuePrefix
	}
	if !strings.HasSuffix(prefix, ".") {
		prefix += "."
	}
	if err := validateQueuePart(prefix, "queue prefix"); err != nil {
		return "", err
	}
	if err := validateContainerID(containerID); err != nil {
		return "", err
	}

	queueName := prefix + containerID + queueSuffix
	if len(queueName) > maxQueueNameLength {
		return "", fmt.Errorf("queue name must be at most %d bytes", maxQueueNameLength)
	}
	return queueName, nil
}

func validateContainerID(containerID string) error {
	if containerID == "" {
		return fmt.Errorf("container ID is required")
	}
	if len(containerID) > maxContainerIDSize {
		return fmt.Errorf("container ID must be at most %d characters", maxContainerIDSize)
	}
	return validateQueuePart(containerID, "container ID")
}

func validateQueuePart(value, name string) error {
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("._-", character) {
			continue
		}
		return fmt.Errorf("%s may contain only letters, numbers, '.', '_' and '-' characters", name)
	}
	return nil
}
