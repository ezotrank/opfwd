package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestConfig for integration tests
type TestConfig struct {
	socketPath      string
	account         string
	allowedCommands []string
	allowedPrefixes []string
}

// setupTestEnvironment creates a test socket path and ensures it doesn't exist
func setupTestEnvironment(t *testing.T) TestConfig {
	t.Helper()

	// Create a temporary directory for the test socket
	tempDir, err := os.MkdirTemp("", "opfwd-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	t.Cleanup(func() {
		os.RemoveAll(tempDir)
	})

	// Create socket path
	socketPath := filepath.Join(tempDir, "opfwd.sock")

	return TestConfig{
		socketPath:      socketPath,
		account:         "test-account",
		allowedCommands: []string{"read op://Employee/CONFIG/operator"},
		allowedPrefixes: []string{"item create"},
	}
}

// mockOpCommand creates a mock 'op' command for testing
func createMockOpCommand(t *testing.T, tempDir string) {
	t.Helper()

	// Create a mock 'op' script that just echoes its arguments
	mockOpPath := filepath.Join(tempDir, "op")
	mockOpContent := `#!/bin/bash
echo "Executing op with args: $@"
if [[ "$*" == *"account get"* ]]; then
		# Simulate authentication status check
		if [[ -f "${HOME}/.op_test_authenticated" ]]; then
				echo "Account is authenticated"
				exit 0
		else
				echo "[ERROR] account is not signed in" >&2
				exit 1
		fi
elif [[ "$*" == *"signin"* ]]; then
		# Simulate successful signin
		mkdir -p "${HOME}"
		touch "${HOME}/.op_test_authenticated"
		echo "Signed in to test-account"
		exit 0
elif [[ "$*" == *"read op://Employee/CONFIG/operator"* ]]; then
		echo "SECRET_VALUE_123"
elif [[ "$*" == *"item create"* ]]; then
		echo "Item created successfully"
else
		echo "Unrecognized command" >&2
		exit 1
fi
`
	err := os.WriteFile(mockOpPath, []byte(mockOpContent), 0755)
	if err != nil {
		t.Fatalf("Failed to create mock op command: %v", err)
	}

	// Add temp dir to PATH
	os.Setenv("PATH", tempDir+":"+os.Getenv("PATH"))

	// Verify it's accessible
	cmd := exec.Command("which", "op")
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), mockOpPath) {
		t.Fatalf("Failed to set up mock 'op' command: %v, output: %s", err, output)
	}

	// Remove any existing authentication marker
	os.Remove(filepath.Join(os.Getenv("HOME"), ".op_test_authenticated"))
}

// startTestServer starts a server instance for testing
func startTestServer(t *testing.T, cfg TestConfig) (context.CancelFunc, <-chan struct{}) {
	t.Helper()

	// Use a temp directory for the mock op command
	tempDir, err := os.MkdirTemp("", "opfwd-bin-*")
	if err != nil {
		t.Fatalf("Failed to create temp bin dir: %v", err)
	}

	t.Cleanup(func() {
		os.RemoveAll(tempDir)
	})

	// Create mock op command
	createMockOpCommand(t, tempDir)

	// Create a context with cancellation for server shutdown
	ctx, cancel := context.WithCancel(context.Background())

	// Channel to signal when server is ready
	ready := make(chan struct{})

	// Start the server in a goroutine
	go func() {
		// Set up the global config
		config = Config{
			SocketPath:      cfg.socketPath,
			Account:         cfg.account,
			AllowedCommands: cfg.allowedCommands,
			AllowedPrefixes: cfg.allowedPrefixes,
		}

		// Set up the socket
		listener, err := setupSocket(cfg.socketPath)
		if err != nil {
			t.Errorf("Failed to set up socket: %v", err)
			return
		}
		defer listener.Close()

		// Signal that server is ready
		close(ready)

		// Start the server
		startServer(ctx, listener)

		// Wait for context cancellation
		<-ctx.Done()
		cleanupSocket()
	}()

	return cancel, ready
}

// sendCommand sends a command to the server and returns the response
func sendCommand(t *testing.T, socketPath, command string) (string, error) {
	t.Helper()

	// Connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return "", fmt.Errorf("failed to connect to socket: %v", err)
	}
	defer conn.Close()

	// Send the command
	_, err = fmt.Fprintf(conn, "%s\n", command)
	if err != nil {
		return "", fmt.Errorf("failed to send command: %v", err)
	}

	// Read the response
	response, err := io.ReadAll(conn)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %v", err)
	}

	return string(response), nil
}

// waitForSocket waits for the socket to become available
func waitForSocket(socketPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := os.Stat(socketPath)
		if err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("socket did not become available within %s", timeout)
}

// TestAllowedExactCommand tests that an exact allowed command works
func TestAllowedExactCommand(t *testing.T) {
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Set up test environment
	cfg := setupTestEnvironment(t)

	// Start the server
	cancel, ready := startTestServer(t, cfg)
	defer cancel()

	// Wait for server to be ready
	<-ready

	// Wait for socket to be available
	err := waitForSocket(cfg.socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("Socket not available: %v", err)
	}

	// Send an allowed command
	response, err := sendCommand(t, cfg.socketPath, "read op://Employee/CONFIG/operator")
	if err != nil {
		t.Fatalf("Failed to send command: %v", err)
	}

	// Check response
	if !strings.Contains(response, "SECRET_VALUE_123") {
		t.Errorf("Expected response to contain 'SECRET_VALUE_123', got: %s", response)
	}
}

// TestAllowedPrefixCommand tests that a command with an allowed prefix works
func TestAllowedPrefixCommand(t *testing.T) {
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Set up test environment
	cfg := setupTestEnvironment(t)

	// Start the server
	cancel, ready := startTestServer(t, cfg)
	defer cancel()

	// Wait for server to be ready
	<-ready

	// Wait for socket to be available
	err := waitForSocket(cfg.socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("Socket not available: %v", err)
	}

	// Send a command with allowed prefix
	response, err := sendCommand(t, cfg.socketPath, "item create document --title='Test Document'")
	if err != nil {
		t.Fatalf("Failed to send command: %v", err)
	}

	// Check response
	if !strings.Contains(response, "Item created successfully") {
		t.Errorf("Expected response to contain 'Item created successfully', got: %s", response)
	}
}

// TestDisallowedCommand tests that a disallowed command is rejected
func TestDisallowedCommand(t *testing.T) {
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Set up test environment
	cfg := setupTestEnvironment(t)

	// Start the server
	cancel, ready := startTestServer(t, cfg)
	defer cancel()

	// Wait for server to be ready
	<-ready

	// Wait for socket to be available
	err := waitForSocket(cfg.socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("Socket not available: %v", err)
	}

	// Send a disallowed command
	response, err := sendCommand(t, cfg.socketPath, "read op://Personal/SSH/passphrase")
	if err != nil {
		t.Fatalf("Failed to send command: %v", err)
	}

	// Check response
	if !strings.Contains(response, "Error: Command not allowed") {
		t.Errorf("Expected error about disallowed command, got: %s", response)
	}
}

// TestMultipleCommands tests sending multiple commands sequentially
func TestMultipleCommands(t *testing.T) {
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Set up test environment
	cfg := setupTestEnvironment(t)

	// Start the server
	cancel, ready := startTestServer(t, cfg)
	defer cancel()

	// Wait for server to be ready
	<-ready

	// Wait for socket to be available
	err := waitForSocket(cfg.socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("Socket not available: %v", err)
	}

	// Send multiple commands
	commands := []string{
		"read op://Employee/CONFIG/operator",
		"item create login --title='Test Login'",
		"read op://Personal/SSH/passphrase", // This should be disallowed
	}

	expectedResponses := []string{
		"SECRET_VALUE_123",
		"Item created successfully",
		"Error: Command not allowed",
	}

	for i, cmd := range commands {
		response, err := sendCommand(t, cfg.socketPath, cmd)
		if err != nil {
			t.Fatalf("Failed to send command %s: %v", cmd, err)
		}

		if !strings.Contains(response, expectedResponses[i]) {
			t.Errorf("Command %s: expected response to contain '%s', got: %s",
				cmd, expectedResponses[i], response)
		}
	}
}

// TestServerCleanupOnShutdown tests that the server cleans up resources when shutting down
func TestServerCleanupOnShutdown(t *testing.T) {
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Set up test environment
	cfg := setupTestEnvironment(t)

	// Start the server
	cancel, ready := startTestServer(t, cfg)

	// Wait for server to be ready
	<-ready

	// Wait for socket to be available
	err := waitForSocket(cfg.socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("Socket not available: %v", err)
	}

	// Verify socket exists
	_, err = os.Stat(cfg.socketPath)
	if err != nil {
		t.Fatalf("Socket file does not exist: %v", err)
	}

	// Shutdown the server
	cancel()

	// Wait a bit for cleanup
	time.Sleep(500 * time.Millisecond)

	// Verify socket is removed
	_, err = os.Stat(cfg.socketPath)
	if !os.IsNotExist(err) {
		t.Errorf("Socket file still exists after shutdown: %v", err)
	}
}
