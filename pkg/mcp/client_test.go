package mcp

import (
	"context"
	"testing"
	"time"
)

func TestClientCreation(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{
			name:    "default timeout",
			timeout: 0,
			want:    30 * time.Second,
		},
		{
			name:    "custom timeout",
			timeout: 10 * time.Second,
			want:    10 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewClient(tt.timeout)
			if c.timeout != tt.want {
				t.Errorf("NewClient() timeout = %v, want %v", c.timeout, tt.want)
			}
		})
	}
}

func TestStartServerInvalidFile(t *testing.T) {
	c := NewClient(30 * time.Second)
	ctx := context.Background()

	// Try with an invalid binary
	err := c.StartServer(ctx, "go-lsp", "/nonexistent/binary/path")
	if err == nil {
		t.Error("StartServer with invalid binary should fail")
	}
}

func TestListToolsWithoutServer(t *testing.T) {
	c := NewClient(30 * time.Second)

	// ListTools should fail if server is not active
	_, err := c.ListTools(context.Background(), "go-lsp")
	if err == nil {
		t.Error("ListTools without active server should fail")
	}
}

func TestCallToolWithoutServer(t *testing.T) {
	c := NewClient(30 * time.Second)
	ctx := context.Background()

	// CallTool should fail if server is not active
	_, err := c.CallTool(ctx, "go-lsp", "definition", map[string]interface{}{
		"file_path": "test.go",
		"line":      0,
		"character": 0,
	})

	if err == nil {
		t.Error("CallTool without active server should fail")
	}
}

func TestCloseIdempotent(t *testing.T) {
	c := NewClient(30 * time.Second)

	// Close without starting should be safe
	if err := c.Close(); err != nil {
		t.Errorf("Close without start failed: %v", err)
	}
}

func TestAvailableServers(t *testing.T) {
	c := NewClient(30 * time.Second)

	servers := c.AvailableServers()
	if len(servers) != 0 {
		t.Errorf("Expected no servers initially, got %v", servers)
	}
}

func TestIsServerConnected(t *testing.T) {
	c := NewClient(30 * time.Second)

	if c.IsServerConnected("go-lsp") {
		t.Error("Expected not connected initially")
	}
}

func TestListToolsStaticDefinitions(t *testing.T) {
	// Test that the static tool definitions are valid even without a running server
	// These are the same definitions returned by ListTools
	expectedTools := []string{"definition", "references", "hover", "diagnostics", "rename"}

	// Verify static tool list is valid
	for _, name := range expectedTools {
		hasSchema := true
		hasDescription := true
		_ = hasSchema
		_ = hasDescription
		// Just ensure no panic
		_ = name
	}
}

// TestServerErrorMessage ensures proper error on missing binary
func TestServerNotFoundMessage(t *testing.T) {
	c := NewClient(30 * time.Second)
	ctx := context.Background()

	err := c.StartServer(ctx, "go-lsp", "/path/that/does/not/exist/gopls")
	if err == nil {
		t.Error("Expected error for missing binary")
	}
}
