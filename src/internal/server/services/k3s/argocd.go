package k3s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ApplicationStatus represents the sync and health status of an ArgoCD Application.
type ApplicationStatus struct {
	SyncStatus   string
	HealthStatus string
}

// GetApplicationStatus queries the K3s cluster for the ArgoCD Application CRD status
// of a specific team and app.
func (c *Client) GetApplicationStatus(
	ctx context.Context,
	teamSlug, appSlug string,
) (ApplicationStatus, error) {
	// Development mode: return healthy defaults when cluster is disabled
	if c.config.DisableNamespaceIsolation {
		return ApplicationStatus{SyncStatus: "Synced", HealthStatus: "Healthy"}, nil
	}

	// Normal mode: query dynamic client
	if c.dynamicClient == nil {
		return ApplicationStatus{}, fmt.Errorf("k3s dynamic client not initialized")
	}

	namespace := fmt.Sprintf("team-%s", teamSlug)
	appName := appSlug

	// Define Application GVR (GroupVersionResource)
	gvr := schema.GroupVersionResource{
		Group:    "argoproj.io",
		Version:  "v1alpha1",
		Resource: "applications",
	}

	// Query the specified namespace for the Application
	unstructured, err := c.dynamicClient.
		Resource(gvr).
		Namespace(namespace).
		Get(ctx, appName, metav1.GetOptions{})
	if err != nil {
		return ApplicationStatus{}, fmt.Errorf("get application %s/%s: %w", namespace, appName, err)
	}

	// Extract status fields
	status := extractApplicationStatus(unstructured.Object)
	return status, nil
}

// extractApplicationStatus parses sync and health status from an Application CRD object.
func extractApplicationStatus(obj map[string]interface{}) ApplicationStatus {
	status := ApplicationStatus{
		SyncStatus:   "Unknown",
		HealthStatus: "Unknown",
	}

	// Navigate: obj["status"]["sync"]["status"]
	objStatus, ok := obj["status"].(map[string]interface{})
	if !ok {
		return status
	}

	// Extract SyncStatus
	syncObj, ok := objStatus["sync"].(map[string]interface{})
	if ok {
		if syncStatus, ok := syncObj["status"].(string); ok {
			status.SyncStatus = syncStatus
		}
	}

	// Extract HealthStatus
	healthObj, ok := objStatus["health"].(map[string]interface{})
	if ok {
		if healthStatus, ok := healthObj["status"].(string); ok {
			status.HealthStatus = healthStatus
		}
	}

	return status
}
