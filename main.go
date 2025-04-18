package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
)

// Config holds the server configuration
type Config struct {
	SocketPath      string
	AccountFlag     string
	AllowedCommands []string
	AllowedPrefixes []string
}

// Command whitelist configurations
var (
	// Global config for access in functions
	config Config
)

// validateCommand checks if a command is allowed based on exact matches or prefix matches
func validateCommand(input string) bool {
	// Get the full command for validation
	cmdWithArgs := strings.TrimSpace(input)

	// Check for exact matches against the allowed commands
	for _, allowed := range config.AllowedCommands {
		if cmdWithArgs == allowed {
			return true
		}
	}

	// Check for prefix matches
	for _, prefix := range config.AllowedPrefixes {
		if strings.HasPrefix(cmdWithArgs, prefix) {
			return true
		}
	}

	return false
}

// handleConnection processes a single client connection
func handleConnection(conn net.Conn) {
	// Recover from panics in the connection handler
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic in connection handler: %v", r)
			conn.Close()
		}
	}()

	defer conn.Close()

	// Read the command with a scanner to handle arbitrary length commands
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		log.Printf("Error reading from connection: %v", scanner.Err())
		return
	}

	input := strings.TrimSpace(scanner.Text())
	log.Printf("Received input: %s", input)

	// Validate the full command
	if !validateCommand(input) {
		log.Printf("Command not allowed: %s", input)
		_, err := conn.Write([]byte(fmt.Sprintf("Error: Command not allowed: %s\n", input)))
		if err != nil {
			log.Printf("Error writing response: %v", err)
		}
		return
	}

	executeCommand(conn, input)
}

// executeCommand runs the op command and pipes output to the connection
func executeCommand(conn net.Conn, input string) {
	// Check if we're logged in first
	if err := ensureLoggedIn(); err != nil {
		log.Printf("Error ensuring login: %v", err)
		_, _ = conn.Write([]byte(fmt.Sprintf("Error: Could not sign in to 1Password: %v\n", err)))
		return
	}

	// Prepare arguments for op command
	args := []string{}

	// Always add the account flag (it's validated as non-empty in main)
	args = append(args, "--account", config.AccountFlag)

	// Add the validated command
	cmdParts := strings.Fields(input)
	args = append(args, cmdParts...)

	logArgs := make([]string, len(args))
	for i, arg := range args {
		logArgs[i] = fmt.Sprintf("'%s'", arg)
	}
	log.Printf("Executing op with args: %s", strings.Join(logArgs, " "))
	opCmd := exec.Command("op", args...)

	// Connect the command's stdout and stderr to the connection
	stdout, err := opCmd.StdoutPipe()
	if err != nil {
		log.Printf("Error creating stdout pipe: %v", err)
		_, _ = conn.Write([]byte(fmt.Sprintf("Error: %v\n", err)))
		return
	}

	stderr, err := opCmd.StderrPipe()
	if err != nil {
		log.Printf("Error creating stderr pipe: %v", err)
		_, _ = conn.Write([]byte(fmt.Sprintf("Error: %v\n", err)))
		return
	}

	// Start the command
	if err := opCmd.Start(); err != nil {
		log.Printf("Error starting command: %v", err)
		_, _ = conn.Write([]byte(fmt.Sprintf("Error: %v\n", err)))
		return
	}

	// Copy output to connection
	go func() {
		_, _ = io.Copy(conn, stdout)
	}()
	go func() {
		_, _ = io.Copy(conn, stderr)
	}()

	// Wait for the command to complete
	if err := opCmd.Wait(); err != nil {
		log.Printf("Command execution error: %v", err)
		// Error already sent via stderr pipe
	}
}

// ensureLoggedIn checks if we're logged in to 1Password and attempts to log in if not
func ensureLoggedIn() error {
	// Try a simple command to check if we're logged in
	checkCmd := exec.Command("op", "--account", config.AccountFlag, "account", "get")

	// We don't care about stdout, just if it exits successfully
	if err := checkCmd.Run(); err == nil {
		// We're already logged in
		log.Println("1Password account is already authenticated")
		return nil
	}

	log.Println("1Password account is not signed in, attempting to sign in")

	// Try to sign in
	signinCmd := exec.Command("op", "signin", "--account", config.AccountFlag)
	output, err := signinCmd.CombinedOutput()

	if err != nil {
		log.Printf("Sign in attempt failed, output: %s", string(output))
		return fmt.Errorf("failed to sign in to 1Password: %v", err)
	}

	log.Println("Successfully signed in to 1Password")
	return nil
}

// cleanupSocket handles socket removal during cleanup
func cleanupSocket() {
	log.Println("Cleaning up and removing socket...")
	if config.SocketPath != "" {
		if err := os.Remove(config.SocketPath); err != nil {
			log.Printf("Failed to remove socket during cleanup: %v", err)
		}
	}
}

// stringSliceFlag is a custom flag type that can be specified multiple times
type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ", ")
}

func (s *stringSliceFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

// parseFlags parses command line flags and returns a Config
func parseFlags() Config {
	var cfg Config

	// Get default socket path
	usr, err := user.Current()
	if err != nil {
		log.Fatalf("Failed to get current user: %v", err)
	}
	defaultSocketPath := filepath.Join(usr.HomeDir, ".ssh", "opfwd.sock")

	// Parse command-line arguments
	flag.StringVar(&cfg.SocketPath, "socket", defaultSocketPath, "Path to the Unix domain socket")
	flag.StringVar(&cfg.AccountFlag, "account", "", "1Password account shorthand to use for all commands (required)")

	// Add flag for repeatable allow-command flag for exact matches
	var allowCommandFlags stringSliceFlag
	flag.Var(&allowCommandFlags, "allow-command", "Command to allow (exact match, can be specified multiple times)")

	// Add new flag for prefix-based command matching
	var allowPrefixFlags stringSliceFlag
	flag.Var(&allowPrefixFlags, "allow-prefix", "Command prefix to allow (can be specified multiple times)")

	flag.Parse()

	// Set allowed commands and prefixes
	cfg.AllowedCommands = allowCommandFlags
	cfg.AllowedPrefixes = allowPrefixFlags

	// Validate that account flag is provided
	if cfg.AccountFlag == "" {
		log.Fatalf("Error: --account flag is required. Please provide your 1Password account shorthand.")
	}

	return cfg
}

// setupSocket creates and configures the Unix domain socket
func setupSocket(socketPath string) (net.Listener, error) {
	// Check if socket file already exists
	if _, err := os.Stat(socketPath); err == nil {
		return nil, fmt.Errorf("Socket file already exists at %s. Another server might be running.\n"+
			"If you're sure no other server is running, remove it manually with: rm %s",
			socketPath, socketPath)
	}

	// Ensure the directory exists
	socketDir := filepath.Dir(socketPath)
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create socket directory: %v", err)
	}

	// Create Unix domain socket
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on socket: %v", err)
	}

	// Set permissions on socket file to only allow the current user
	if err := os.Chmod(socketPath, 0600); err != nil {
		listener.Close()
		os.Remove(socketPath)
		return nil, fmt.Errorf("failed to set permissions on socket: %v", err)
	}

	return listener, nil
}

// setupSignalHandling sets up graceful shutdown on signals
func setupSignalHandling(cancel context.CancelFunc, listener net.Listener) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutting down server...")
		cancel() // Cancel the context to signal shutdown
		listener.Close()
		cleanupSocket()
	}()
}

// startServer accepts and handles connections
func startServer(ctx context.Context, listener net.Listener) {
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				if ctx.Err() != nil {
					// Context was cancelled, server is shutting down
					return
				}
				log.Printf("Error accepting connection: %v", err)
				continue
			}

			go handleConnection(conn)
		}
	}()
}

func main() {
	// Set up recovery for panics in main
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic in main: %v", r)
			cleanupSocket()
		}
	}()

	// Parse command line flags
	config = parseFlags()

	// Set up the socket
	listener, err := setupSocket(config.SocketPath)
	if err != nil {
		log.Fatalf("Failed to set up socket: %v", err)
	}
	defer listener.Close()

	// Log configuration
	log.Printf("Server listening on %s", config.SocketPath)
	log.Printf("Allowed exact commands: %v", config.AllowedCommands)
	log.Printf("Allowed command prefixes: %v", config.AllowedPrefixes)
	log.Printf("Using 1Password account: %s", config.AccountFlag)

	// Set up context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling for graceful shutdown
	setupSignalHandling(cancel, listener)

	// Start the server
	startServer(ctx, listener)

	// Wait for context cancellation (i.e., shutdown signal)
	<-ctx.Done()
	log.Println("Server shutdown completed")
}
