package k3s

import "testing"

func TestNewClient_DisableNamespaceIsolation_ReturnsClient(t *testing.T) {
	t.Setenv("KUBECONFIG", "")

	client, err := NewClient(&Config{DisableNamespaceIsolation: true})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("NewClient() returned nil client")
	}
}

func TestNewClient_InvalidKubeconfigPath_ReturnsError(t *testing.T) {
	client, err := NewClient(&Config{
		KubeconfigPath:            "/path/that/does/not/exist",
		DisableNamespaceIsolation: false,
	})
	if err == nil {
		t.Fatalf("NewClient() error = nil, client=%v; want error", client)
	}
}
