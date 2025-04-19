# opfwd - 1Password CLI Forwarding

opfwd allows for seamless access to 1Password passwords from remote Linux machines by forwarding `op` CLI commands to a local MacOS machine where you're already authenticated with 1Password.

## Overview

When working with remote Linux VMs or servers, 1Password CLI authentication can be challenging - sessions expire every 30 minutes and require re-authentication. opfwd solves this by:

1. Running a server on your MacOS machine where 1Password is already authenticated
2. Forwarding commands from your Linux machine through an SSH tunnel
3. Executing them on your MacOS and returning the results

This eliminates the need to re-authenticate with `op` on your remote Linux machines.

## Why opfwd?

opfwd provides several key advantages:

1. **Offline Access**: Since the 1Password app on MacOS can operate offline with a local vault cache, opfwd allows you to access your 1Password secrets from Linux machines even without internet connectivity (as long as your MacOS and Linux machines can communicate via SSH).

2. **No Re-authentication**: Eliminates the need to constantly re-authenticate the `op` CLI on Linux machines every 30 minutes or when starting new terminal sessions.

3. **Security Isolation**: Keeps your 1Password authentication confined to your trusted MacOS device rather than having it on multiple remote machines.

4. **Simplified Workflow**: Use 1Password seamlessly across your development environment without interruptions for authentication.

5. **Command Filtering**: Limit which 1Password items can be accessed from remote machines, providing an extra layer of security.

## Features

- Secure command forwarding via SSH and Unix domain sockets
- Command whitelisting for enhanced security with exact and prefix matching
- Automatic socket handling and cleanup
- Support for 1Password account selection
- Customizable allowed commands list and prefixes
- Offline functionality (as long as your MacOS has a local vault cache)
- Single binary for both server and client operations

## Installation

### MacOS (Server)

Install using Homebrew:
```bash
brew install ezotrank/tools/opfwd
```

### Linux (Client)

Install the RPM package from the latest release. For example:
```bash
sudo rpm -i https://github.com/ezotrank/opfwd/releases/download/v0.1.10/opfwd_0.1.10_linux_arm64.rpm
```

Add this to your `~/.bashrc` to automatically set up the `op` command:
```bash
if test -f /usr/bin/opfwd && ! test -f ~/.local/bin/op; then
    ln -s /usr/bin/opfwd ~/.local/bin/op
fi
```

### Building from Source

Alternatively, you can build the binary from source:

```bash
go build -o opfwd main.go
```

### Server Configuration (Linux)

For proper socket handling, ensure your SSH server (sshd) on the Linux machine is configured to automatically remove stale socket files. Add the following to your `/etc/ssh/sshd_config`:

```bash
StreamLocalBindUnlink yes
```

This setting allows SSH to automatically remove existing socket files when setting up socket forwarding, which prevents "socket already exists" errors when reconnecting.

After making this change, restart the SSH server:

```bash
sudo systemctl restart sshd
```

## SSH Configuration

Add this to your `~/.ssh/config` file on your MacOS machine:

```
Host your-linux-server
    HostName your-server-hostname-or-ip
    User your-username
    # Forward the Unix domain socket
    RemoteForward /home/your-username/.ssh/opfwd.sock /Users/your-macos-username/.ssh/opfwd.sock
    StreamLocalBindUnlink yes
    ExitOnForwardFailure yes
    ControlMaster auto
    ControlPath ~/.ssh/control-%r@%h:%p
    ControlPersist yes
    ServerAliveCountMax 3
    ServerAliveInterval 15
```

## Usage

### Starting the Server (MacOS)

Start the server on your MacOS machine with:

```bash
opfwd --server
```

The server uses a YAML configuration file (default location: `~/.config/opfwd/config.yaml`). You can specify a different config location with:

```bash
opfwd --server --config=/path/to/config.yaml
```

Configuration file format:

```yaml
# 1Password account shorthand (required)
account: "your-1password-account"

# Socket path (optional, defaults to ~/.ssh/opfwd.sock)
socket_path: "/path/to/socket.sock"

# List of exact commands to allow
allowed_commands:
  - "read op://Personal/SSH/passphrase"
  - "read op://Work/API/token"

# List of command prefixes to allow
allowed_prefixes:
  - "read op://Personal/SSH/"
  - "read op://Work/API/"
```

Example configurations:

```yaml
# Allow specific commands only
account: "my-account"
allowed_commands:
  - "read op://Personal/SSH/passphrase"
  - "read op://Work/API/token"

# Allow commands with specific prefixes
account: "my-account"
allowed_prefixes:
  - "read op://Personal/SSH/"

# Combine exact and prefix matches
account: "my-account"
allowed_commands:
  - "read op://Personal/SSH/passphrase"
allowed_prefixes:
  - "read op://Work/API/"
```

**Important Notes on Command Whitelisting:**

- `allowed_commands` allows _exact_ matches. This means the full command string, including any arguments, must match exactly.
- `allowed_prefixes` allows commands that _start with_ the specified prefix. This allows more flexibility when the command structure is predictable, but the specific item details might vary. For example, allowing the prefix "read op://Work/" would allow reading any item in the "Work" vault. Be careful when using prefixes as they can potentially expose more secrets than intended.
- For security best practices, it's recommended to start with specific `allowed_commands` rules and only use `allowed_prefixes` when necessary, and as restrictively as possible.

### Connecting to Linux Server

Connect to your Linux server with SSH, which will establish the socket forwarding:

```bash
ssh your-linux-server
```

### Using `op` on Linux

Now you can use `op` commands on your Linux machine as if they were running locally:

```bash
op read op://Employee/SOME-CONFIG/operator
```

The command will be forwarded to your MacOS machine, executed there using your existing 1Password session, and the results will be returned to your Linux shell.

## Offline Operation

One of the key benefits of opfwd is the ability to access 1Password items without internet connectivity:

1. The 1Password desktop app on MacOS maintains a local cache of your vault data
2. This allows the `op` CLI on MacOS to access items even when offline
3. By forwarding commands through opfwd, your Linux machine can access these items without internet connectivity
4. As long as your MacOS and Linux machines can communicate over SSH, you can retrieve passwords, tokens, and other secrets even in offline environments

This is particularly valuable in:

- Development environments with limited connectivity
- Cloud deployments requiring secret retrieval during network outages
- Secure environments with air-gapped networks
- Travel scenarios with intermittent internet access

## Security Considerations

- **Command Whitelisting**: By default, only specific commands or command prefixes are allowed. Use `allowed_commands` to specify permitted commands for exact matches, and `allowed_prefixes` for commands that start with a specific prefix.
- **Socket Permissions**: The Unix socket is created with 0600 permissions to restrict access to the current user only.
- **SSH Encryption**: All communication between Linux and MacOS happens over encrypted SSH connections.
- **No Persistent Storage**: opfwd doesn't store 1Password secrets or session tokens. The 1Password session lives on your macOS machine and is never transmitted to or stored on the Linux client.
- **Careful Prefix Usage**: When using `allowed_prefixes`, ensure the prefix is as specific as possible to limit potential exposure of unintended secrets.

## Troubleshooting

### Socket Not Found

If you see `Error: Socket not found`, make sure:

1. The SSH connection is active
2. Socket forwarding is properly configured in your SSH config
3. The opfwd server is running on your MacOS

### Command Not Allowed

If you see `Error: Command not allowed`, the command you're trying to execute is not in the whitelist. Add it to your configuration file under either `allowed_commands` for an exact match or `allowed_prefixes` to allow commands starting with a specific prefix.

### Socket Already Exists

If the server reports `Socket file already exists`, either:

1. Another opfwd server is already running
2. A previous server didn't clean up properly. Remove the socket with `rm ~/.ssh/opfwd.sock`

## Limitations

- Commands must be explicitly whitelisted for security reasons, either with exact matches or using prefixes.
- Remote forwarding must be allowed on the SSH server
- Requires an active SSH connection
- Your MacOS computer must be running and accessible

## License

MIT License

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.