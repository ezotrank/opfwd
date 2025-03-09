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

// Command whitelist - these are the allowed op commands (exact matches)
var allowedFullCommands = []string{}

// Global socket path for cleanup in recovery functions
var socketPath string
var accountFlag string

func validateCommand(input string) bool {
	// Get the full command for validation
	cmdWithArgs := strings.TrimSpace(input)

	// Check for exact matches against the allowed commands
	for _, allowed := range allowedFullCommands {
		if cmdWithArgs == allowed {
			return true
		}
	}
	return false
}

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

	// Prepare arguments for op command
	args := []string{}

	// Always add the account flag (it's validated as non-empty in main)
	args = append(args, "--account", accountFlag)

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

// cleanupSocket handles socket removal during cleanup
func cleanupSocket() {
	log.Println("Cleaning up and removing socket...")
	if socketPath != "" {
		if err := os.Remove(socketPath); err != nil {
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

func main() {
	// Get default socket path
	usr, err := user.Current()
	if err != nil {
		log.Fatalf("Failed to get current user: %v", err)
	}
	defaultSocketPath := filepath.Join(usr.HomeDir, ".ssh", "opfwd.sock")

	// Parse command-line arguments
	flag.StringVar(&socketPath, "socket", defaultSocketPath, "Path to the Unix domain socket")
	flag.StringVar(&accountFlag, "account", "", "1Password account shorthand to use for all commands (required)")

	// Add flag for repeatable allow-command flag
	var allowCommandFlags stringSliceFlag
	flag.Var(&allowCommandFlags, "allow-command", "Command to allow (can be specified multiple times)")

	flag.Parse()

	// If allowed commands were provided via flags, override the default list
	if len(allowCommandFlags) > 0 {
		// Use the commands specified with --allow-command flags
		allowedFullCommands = allowCommandFlags
	}

	// Validate that account flag is provided
	if accountFlag == "" {
		log.Fatalf("Error: --account flag is required. Please provide your 1Password account shorthand.")
	}
	flag.Parse()

	// Set up recovery for panics in main
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic in main: %v", r)
			cleanupSocket()
		}
	}()

	// Check if socket file already exists
	if _, err := os.Stat(socketPath); err == nil {
		log.Fatalf("Socket file already exists at %s. Another server might be running.\n"+
			"If you're sure no other server is running, remove it manually with: rm %s",
			socketPath, socketPath)
	}

	// Ensure the directory exists
	socketDir := filepath.Dir(socketPath)
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		log.Fatalf("Failed to create socket directory: %v", err)
	}

	// Create Unix domain socket
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("Failed to listen on socket: %v", err)
	}
	defer listener.Close()

	// Set permissions on socket file to only allow the current user
	if err := os.Chmod(socketPath, 0600); err != nil {
		log.Printf("Failed to set permissions on socket: %v", err)
		cleanupSocket()
		log.Fatalf("Failed to set permissions on socket: %v", err)
	}

	log.Printf("Server listening on %s", socketPath)
	log.Printf("Allowed command prefixes: %v", allowedFullCommands)
	if accountFlag != "" {
		log.Printf("Using 1Password account: %s", accountFlag)
	}

	// Use context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Run a goroutine to handle shutdown signals
	go func() {
		<-sigChan
		log.Println("Shutting down server...")
		cancel() // Cancel the context to signal shutdown
		listener.Close()
		cleanupSocket()
	}()

	// Accept connections until context is cancelled
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

			// Pass the context to connection handlers
			go func(c net.Conn) {
				handleConnection(c)
			}(conn)
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()
	log.Println("Server shutdown completed")
}
