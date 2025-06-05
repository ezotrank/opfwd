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
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"

	"gopkg.in/yaml.v3"
)

// Version information
var (
	version   = "dev"
	commit    = "none"
	buildDate = ""
	goVersion = runtime.Version()
)

func initVersion() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}

	modified := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			commit = setting.Value
		case "vcs.time":
			buildDate = setting.Value
		case "vcs.modified":
			if setting.Value == "true" {
				modified = true
			}
		}
	}
	if modified {
		commit += "+CHANGES"
	}
}

// Config holds the server configuration
type Config struct {
	SocketPath      string   `yaml:"socket_path"`
	Account         string   `yaml:"account"`
	AllowedCommands []string `yaml:"allowed_commands"`
	AllowedPrefixes []string `yaml:"allowed_prefixes"`
}

// Global config for access in functions
var config Config

// loadConfig loads configuration from YAML file
func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config file: %w", err)
	}

	// Validate required fields
	if cfg.Account == "" {
		return Config{}, fmt.Errorf("account is required in config")
	}

	// Set default socket path if not specified
	if cfg.SocketPath == "" {
		usr, err := user.Current()
		if err != nil {
			return Config{}, fmt.Errorf("getting current user: %w", err)
		}
		cfg.SocketPath = filepath.Join(usr.HomeDir, ".ssh", "opfwd.sock")
	}

	return cfg, nil
}

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

	// Always add the account flag
	args = append(args, "--account", config.Account)

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
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		if _, err := io.Copy(conn, stdout); err != nil {
			log.Printf("Error copying stdout: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := io.Copy(conn, stderr); err != nil {
			log.Printf("Error copying stderr: %v", err)
		}
	}()

	// Wait for the command to complete
	if err := opCmd.Wait(); err != nil {
		log.Printf("Command execution error: %v", err)
		// Error already sent via stderr pipe
	}

	// Wait for all output to be copied before closing connection
	wg.Wait()
}

// ensureLoggedIn checks if we're logged in to 1Password and attempts to log in if not
func ensureLoggedIn() error {
	// Try a simple command to check if we're logged in
	checkCmd := exec.Command("op", "--account", config.Account, "account", "get")

	// We don't care about stdout, just if it exits successfully
	if err := checkCmd.Run(); err == nil {
		// We're already logged in
		log.Println("1Password account is already authenticated")
		return nil
	}

	log.Println("1Password account is not signed in, attempting to sign in")

	// Try to sign in
	signinCmd := exec.Command("op", "signin", "--account", config.Account)
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

// getDefaultConfigPath returns the default path to the config file
func getDefaultConfigPath() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("getting current user: %w", err)
	}
	return filepath.Join(usr.HomeDir, ".config", "opfwd", "config.yaml"), nil
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

// runServer starts the server mode of the application
func runServer(configPath string) {
	// Set up recovery for panics in main
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic in main: %v", r)
			cleanupSocket()
		}
	}()

	// Check if the 'op' command exists
	if _, err := exec.LookPath("op"); err != nil {
		log.Fatalf("The 1Password CLI (op) command was not found in your system PATH.\n\nTo install it on macOS:\n\nbrew install 1password-cli\n\nError details: %v", err)
	}

	// Load configuration
	var err error
	config, err = loadConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

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
	log.Printf("Using 1Password account: %s", config.Account)

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

// getDefaultSocketPath returns the default path to the socket file
func getDefaultSocketPath() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("getting current user: %w", err)
	}
	return filepath.Join(usr.HomeDir, ".ssh", "opfwd.sock"), nil
}

// runClient handles the client mode of the application
func runClient() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: opfwd <command> [arguments]")
		os.Exit(1)
	}

	socketPath, err := getDefaultSocketPath()
	if err != nil {
		fmt.Printf("Error getting default socket path: %v\n", err)
		os.Exit(1)
	}

	// Check if the socket exists
	if _, err := os.Stat(socketPath); err != nil {
		fmt.Printf("Error: Socket %s not found.\n", socketPath)
		fmt.Println("Make sure the opfwd server is running and the socket is accessible.")
		os.Exit(1)
	}

	// Connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		fmt.Printf("Error connecting to socket: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Send the command to the server
	command := strings.Join(os.Args[1:], " ")
	if _, err := fmt.Fprintln(conn, command); err != nil {
		fmt.Printf("Error sending command: %v\n", err)
		os.Exit(1)
	}

	// Read and display the response
	if _, err := io.Copy(os.Stdout, conn); err != nil {
		fmt.Printf("Error reading response: %v\n", err)
		os.Exit(1)
	}
}

func main() {
	// Define flags
	serverMode := flag.Bool("server", false, "Run in server mode")
	configPath := flag.String("config", "", "Path to the config file (server mode only)")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.Parse()

	// Initialize version information
	initVersion()

	if *showVersion {
		fmt.Printf("opfwd version %s\n", version)
		fmt.Printf("Commit: %s\n", commit)
		if buildDate != "" {
			fmt.Printf("Build Date: %s\n", buildDate)
		}
		fmt.Printf("Go Version: %s\n", goVersion)
		return
	}

	if *serverMode {
		// If no config path specified, use default
		if *configPath == "" {
			defaultPath, err := getDefaultConfigPath()
			if err != nil {
				log.Fatalf("Failed to get default config path: %v", err)
			}
			*configPath = defaultPath
		}
		runServer(*configPath)
	} else {
		// Client mode
		runClient()
	}
}
