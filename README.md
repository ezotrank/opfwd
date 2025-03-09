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
- Command whitelisting for enhanced security
- Automatic socket handling and cleanup
- Support for 1Password account selection
- Customizable allowed commands list
- Offline functionality (as long as your MacOS has a local vault cache)

## Installation

### MacOS (Server)

1. Build the server:

   ```bash
   go build -o opfwd main.go
   ```

2. Install the binary to a location in your PATH:
   ```bash
   cp opfwd /usr/local/bin/
   ```

### Linux (Client)

The client script is included in the repository as `op`. To install it:

1. Copy the script to a location in your PATH:

   ```bash
   # Copy to your personal bin directory
   cp op ~/bin/op

   # Or install system-wide (requires root)
   sudo cp op /usr/local/bin/op
   ```

2. Make it executable:

   ```bash
   chmod +x ~/bin/op  # Or /usr/local/bin/op if installed system-wide
   ```

3. Verify the installation:
   ```bash
   which op
   ```

The script requires `netcat` to be installed on your Linux system. If it's not already installed, you can install it with:

```bash
# Debian/Ubuntu
sudo apt install netcat

# RHEL/CentOS/Fedora
sudo dnf install nmap-ncat

# Alpine Linux
apk add netcat-openbsd
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
    # Keep the connection alive
    ServerAliveInterval 60
    ServerAliveCountMax 3
```

## Usage

### Starting the Server (MacOS)

Start the server on your MacOS machine with:

```bash
opfwd --account=your-1password-account
```

Options:

- `--socket=/path/to/socket` - Socket path (default: ~/.ssh/opfwd.sock)
- `--account=account` - 1Password account shorthand (required)
- `--allow-command="command"` - Allowed command (can be specified multiple times)

Example with custom commands:

```bash
opfwd --account=my-account \
  --allow-command="read op://Personal/SSH/passphrase" \
  --allow-command="read op://Work/API/token"
```

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

- **Command Whitelisting**: By default, only specific commands are allowed. Use `--allow-command` to specify permitted commands.
- **Socket Permissions**: The Unix socket is created with 0600 permissions to restrict access to the current user only.
- **SSH Encryption**: All communication between Linux and MacOS happens over encrypted SSH connections.
- **No Persistent Storage**: opfwd doesn't store 1Password secrets or session tokens.

## Troubleshooting

### Socket Not Found

If you see `Error: Socket not found`, make sure:

1. The SSH connection is active
2. Socket forwarding is properly configured in your SSH config
3. The opfwd server is running on your MacOS

### Command Not Allowed

If you see `Error: Command not allowed`, the command you're trying to execute is not in the whitelist. Add it using `--allow-command` when starting the server.

### Socket Already Exists

If the server reports `Socket file already exists`, either:

1. Another opfwd server is already running
2. A previous server didn't clean up properly. Remove the socket with `rm ~/.ssh/opfwd.sock`

### Netcat Not Installed

If you see `Error: 'nc' (netcat) is not installed`, follow the installation instructions provided in the error message to install netcat on your Linux system.

## Limitations

- Commands must be explicitly whitelisted for security reasons
- Remote forwarding must be allowed on the SSH server
- Requires an active SSH connection
- Your MacOS computer must be running and accessible

## License

MIT License

## Contributing
