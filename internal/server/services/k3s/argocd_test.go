package k3s

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractApplicationStatus_Synced_Healthy(t *testing.T) {
	// Arrange
	obj := map[string]interface{}{
		"status": map[string]interface{}{
			"sync": map[string]interface{}{
				"status": "Synced",
			},
			"health": map[string]interface{}{
				"status": "Healthy",
			},
		},
	}

	// Act
	result := extractApplicationStatus(obj)

	// Assert
	assert.Equal(t, "Synced", result.SyncStatus)
	assert.Equal(t, "Healthy", result.HealthStatus)
}

func TestExtractApplicationStatus_OutOfSync_Degraded(t *testing.T) {
	// Arrange
	obj := map[string]interface{}{
		"status": map[string]interface{}{
			"sync": map[string]interface{}{
				"status": "OutOfSync",
			},
			"health": map[string]interface{}{
				"status": "Degraded",
			},
		},
	}

	// Act
	result := extractApplicationStatus(obj)

	// Assert
	assert.Equal(t, "OutOfSync", result.SyncStatus)
	assert.Equal(t, "Degraded", result.HealthStatus)
}

func TestExtractApplicationStatus_Missing_Status(t *testing.T) {
	// Arrange - empty object
	obj := map[string]interface{}{}

	// Act
	result := extractApplicationStatus(obj)

	// Assert - defaults to Unknown
	assert.Equal(t, "Unknown", result.SyncStatus)
	assert.Equal(t, "Unknown", result.HealthStatus)
}

func TestExtractApplicationStatus_Missing_Sync_Field(t *testing.T) {
	// Arrange - only health present
	obj := map[string]interface{}{
		"status": map[string]interface{}{
			"health": map[string]interface{}{
				"status": "Healthy",
			},
		},
	}

	// Act
	result := extractApplicationStatus(obj)

	// Assert
	assert.Equal(t, "Unknown", result.SyncStatus)
	assert.Equal(t, "Healthy", result.HealthStatus)
}

func TestExtractApplicationStatus_Missing_Health_Field(t *testing.T) {
	// Arrange - only sync present
	obj := map[string]interface{}{
		"status": map[string]interface{}{
			"sync": map[string]interface{}{
				"status": "Synced",
			},
		},
	}

	// Act
	result := extractApplicationStatus(obj)

	// Assert
	assert.Equal(t, "Synced", result.SyncStatus)
	assert.Equal(t, "Unknown", result.HealthStatus)
}

func TestGetApplicationStatus_DisabledMode_Returns_Synced_Healthy(t *testing.T) {
	// Arrange
	c := &Client{
		config: &Config{DisableNamespaceIsolation: true},
	}

	// Act
	result, err := c.GetApplicationStatus(context.Background(), "acme", "demo")

	// Assert
	assert.NoError(t, err)
	assert.Equal(t, "Synced", result.SyncStatus)
	assert.Equal(t, "Healthy", result.HealthStatus)
}

func TestGetApplicationStatus_No_Dynamic_Client_Error(t *testing.T) {
	// Arrange
	c := &Client{
		config:        &Config{DisableNamespaceIsolation: false},
		dynamicClient: nil,
	}

	// Act
	_, err := c.GetApplicationStatus(context.Background(), "acme", "demo")

	// Assert
	assert.Error(t, err)
	assert.Equal(t, "k3s dynamic client not initialized", err.Error())
}

