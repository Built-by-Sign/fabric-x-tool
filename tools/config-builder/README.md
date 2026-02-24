# config-builder - Fabric-X Network Configuration Tool

A comprehensive CLI tool for building, configuring, and managing Fabric-X blockchain networks. This tool automates the generation of cryptographic materials, genesis blocks, node configurations, and Docker Compose files.

## Table of Contents

- [Features](#features)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Commands](#commands)
- [Configuration](#configuration)
- [Advanced Usage](#advanced-usage)
- [Troubleshooting](#troubleshooting)
- [Development](#development)

## Features

- **Automated Network Setup**: Generate all required artifacts for a Fabric-X network
- **Crypto Material Generation**: Create certificates and keys using cryptogen or Fabric CA
- **KMS Integration**: Support for remote HSM access via KMS
- **TLS Support**: Configure TLS and mutual TLS for secure communication
- **Genesis Block Generation**: Create genesis blocks using configtxgen
- **Node Configuration**: Generate configuration files for orderers, peers, and committers
- **Docker Compose Generation**: Create docker-compose.yaml for easy network deployment
- **Flexible Logging**: Multiple log levels for different use cases
- **Cross-Platform**: Build for Linux, macOS, and Windows

## Installation

### Build from Source

```bash
cd tools/config-builder
make build
```

The binary will be created at `./build/config-builder`.

### Install to System

```bash
make install
```

This installs the binary to `$GOPATH/bin`.

### Build for Multiple Platforms

```bash
make build-all
```

This creates binaries for:
- Linux (amd64, arm64)
- macOS (amd64, arm64)

## Quick Start

### 1. Create a Network Configuration File

Create a YAML configuration file (e.g., `network.yaml`):

```yaml
project_dir: /path/to/fabric-x-orderer
output_dir: ./out
channel_id: arma
cli_version: latest

# TLS Configuration
tls:
  enabled: true
  client_auth_required: false

# Docker Configuration
docker:
  name: cbdc
  network: cbdc_net
  network_driver: bridge
  orderer_image: hyperledger/fabric-x-orderer:local
  committer_image: hyperledger/fabric-x-committer:local
  tools_image: docker.io/hyperledger/fabric-x-tools:0.0.4
  use_local_tools: false

# Orderer Organizations
orderer_orgs:
  - name: Orderer
    domain: example.com
    enable_organizational_units: false
    orderers:
      - name: orderer0
        type: router
        port: 7050
        host: orderer0.example.com
      - name: orderer1
        type: batcher
        port: 7051
        host: orderer1.example.com
        shard_id: 0

# Peer Organizations
peer_orgs:
  - name: Org1
    domain: org1.example.com
    enable_organizational_units: false
    peers:
      - name: peer0
        type: peer
        port: 7051
        host: peer0.org1.example.com
    users:
      - name: Admin
      - name: User1

# Committer Configuration
committer:
  use_postgres: true
  components:
    - name: db
      type: db
      port: 5432
      host: db
      postgres_user: postgres
      postgres_password: password
      postgres_db: cbdc
    - name: validator
      type: validator
      port: 8080
      host: validator
```

### 2. Generate Network Artifacts

```bash
./build/config-builder setup -c network.yaml -o ./out
```

This command generates:
- Cryptographic materials (certificates and keys)
- Shared configuration (shared_config.binpb)
- Genesis block
- Node configuration files

### 3. Generate Docker Compose File

```bash
./build/config-builder gen-compose -c network.yaml -o ./out
```

This creates a `docker-compose.yaml` file in the output directory.

### 4. Start the Network

```bash
cd ./out
docker-compose up -d
```

## Commands

### setup

Generate all required artifacts for the Fabric-X network.

```bash
config-builder setup [flags]
```

**Flags:**
- `-c, --config string`: Network configuration file (default: "network.yaml")
- `-o, --output string`: Output directory (default: "./out")
- `--log-level string`: Log level: quiet, info, verbose, debug (default: "info")
- `--progress`: Show progress information (default: true)
- `--use-local-tools`: Use local tools instead of Docker (requires tools in PATH)

**Example:**
```bash
config-builder setup -c network.yaml -o ./out --log-level=verbose
```

### gen-compose

Generate docker-compose.yaml file for the network.

```bash
config-builder gen-compose [flags]
```

**Flags:**
- `-c, --config string`: Network configuration file (default: "network.yaml")
- `-o, --output string`: Output directory (default: "./out")
- `--log-level string`: Log level: quiet, info, verbose, debug (default: "info")

**Example:**
```bash
config-builder gen-compose -c network.yaml -o ./out
```

### generate

Generate specific configuration files without running full setup.

```bash
config-builder generate [type] [flags]
```

**Types:**
- `crypto-config`: Generate crypto-config.yaml for cryptogen
- `configtx`: Generate configtx.yaml for configtxgen
- `node-config`: Generate node configuration files
- `docker-compose`: Generate docker-compose.yaml

**Example:**
```bash
config-builder generate crypto-config -c network.yaml -o ./out
```

### completion

Generate shell completion scripts.

```bash
config-builder completion [bash|zsh|fish|powershell]
```

**Examples:**

**Bash:**
```bash
# Temporary (current session)
source <(config-builder completion bash)

# Permanent
config-builder completion bash > /etc/bash_completion.d/config-builder
```

**Zsh:**
```bash
# Temporary (current session)
source <(config-builder completion zsh)

# Permanent
config-builder completion zsh > "${fpath[1]}/_config-builder"
```

**Fish:**
```bash
# Permanent
config-builder completion fish > ~/.config/fish/completions/config-builder.fish
```

## Configuration

### Network Configuration Structure

The network configuration file is a YAML file with the following structure:

#### Global Settings

```yaml
project_dir: /path/to/fabric-x-orderer  # Path to fabric-x-orderer repository
output_dir: ./out                        # Output directory for generated files
channel_id: arma                         # Channel ID
cli_version: latest                      # CLI version
```

#### TLS Configuration

```yaml
tls:
  enabled: true                    # Enable TLS for orderer nodes
  client_auth_required: false      # Require client authentication (mTLS)
```

#### KMS Configuration

For remote HSM access via KMS:

```yaml
kms:
  enabled: true
  endpoint: kms.example.com:50051
  token_label: fabric-x
  ca_url: https://ca.example.com:7054
```

#### Organization Configuration

**Orderer Organizations:**

```yaml
orderer_orgs:
  - name: Orderer
    domain: example.com
    enable_organizational_units: false
    kms_token_label: orderer-token    # Optional: KMS token label
    kms_user_pin: "1234"              # Optional: KMS user PIN
    orderers:
      - name: orderer0
        type: router                   # router, batcher, consenter, assembler
        port: 7050
        host: orderer0.example.com
        shard_id: 0                    # Optional: for sharded nodes
        user_pin: "1234"               # Optional: per-node KMS PIN
```

**Peer Organizations:**

```yaml
peer_orgs:
  - name: Org1
    domain: org1.example.com
    enable_organizational_units: false
    kms_token_label: org1-token       # Optional: KMS token label
    kms_user_pin: "1234"              # Optional: KMS user PIN
    peers:
      - name: peer0
        type: peer
        port: 7051
        host: peer0.org1.example.com
    users:
      - name: Admin
        meta_namespace_admin: true    # Optional: namespace admin privilege
      - name: User1
```

#### Committer Configuration

```yaml
committer:
  use_postgres: true
  components:
    - name: db
      type: db                        # db, validator, verifier, coordinator, sidecar, query-service
      port: 5432
      host: db
      postgres_user: postgres         # For db type
      postgres_password: password     # For db type
      postgres_db: cbdc               # For db type
    - name: validator
      type: validator
      port: 8080
      host: validator
```

#### Docker Configuration

```yaml
docker:
  name: cbdc
  network: cbdc_net
  network_driver: bridge
  network_external: false
  orderer_image: hyperledger/fabric-x-orderer:local
  committer_image: hyperledger/fabric-x-committer:local
  tools_image: docker.io/hyperledger/fabric-x-tools:0.0.4
  use_local_tools: false              # Use local tools instead of Docker
```

## Advanced Usage

### Log Levels

Control the verbosity of output with the `--log-level` flag:

| Level | Output | Use Case |
|-------|--------|----------|
| `quiet` | Errors only | CI/CD pipelines, automation |
| `info` | Key steps (default) | Normal usage, production |
| `verbose` | Detailed operations | Development, debugging |
| `debug` | All debug information | Troubleshooting, deep debugging |

**Examples:**

```bash
# CI/CD mode (minimal output)
config-builder setup -c network.yaml -o ./out --log-level=quiet --progress=false

# Debug mode (maximum output)
config-builder setup -c network.yaml -o ./out --log-level=debug

# Verbose mode (detailed but not overwhelming)
config-builder setup -c network.yaml -o ./out --log-level=verbose
```

### Using Local Tools

By default, config-builder uses Docker containers to run tools like cryptogen and configtxgen. You can use locally installed tools instead:

```bash
config-builder setup -c network.yaml -o ./out --use-local-tools
```

Or configure it in the YAML file:

```yaml
docker:
  use_local_tools: true
```

**Requirements:**
- `cryptogen` must be in PATH
- `configtxgen` must be in PATH
- `fabric-ca-client` must be in PATH (if using Fabric CA)
- `armageddon` must be in PATH or will be installed via `go install`

### KMS Integration

When using KMS for remote HSM access:

1. **Enable KMS in configuration:**

```yaml
kms:
  enabled: true
  endpoint: kms.example.com:50051
  token_label: fabric-x
  ca_url: https://ca.example.com:7054
```

2. **Configure organization-level tokens:**

```yaml
orderer_orgs:
  - name: Orderer
    kms_token_label: orderer-token
    kms_user_pin: "1234"
```

3. **Optional: Configure per-node PINs:**

```yaml
orderers:
  - name: orderer0
    user_pin: "5678"  # Overrides organization-level PIN
```

### Environment Variables for Debugging

#### KMS_SO_DEBUG

Enable KMS shared library debug output:

```bash
export KMS_SO_DEBUG=1
config-builder setup -c network.yaml -o ./out
```

#### GRPC_HELPER_DEBUG

Enable gRPC client debug output:

```bash
export GRPC_HELPER_DEBUG=1
config-builder setup -c network.yaml -o ./out
```

#### Combined Debugging

```bash
export KMS_SO_DEBUG=1
export GRPC_HELPER_DEBUG=1
config-builder setup -c network.yaml -o ./out --log-level=debug
```

## Troubleshooting

### Common Issues

#### 1. HSM/SoftHSM Errors

**Problem:** `CKR_TOKEN_NOT_PRESENT` or similar HSM errors

**Solution:**
```bash
# Check SoftHSM configuration
cat /tmp/softhsm2.conf

# List available slots
softhsm2-util --show-slots

# Initialize token if needed
softhsm2-util --init-token --slot 0 --label "fabric-x" --so-pin 1234 --pin 1234
```

#### 2. Docker Image Not Found

**Problem:** `Error response from daemon: pull access denied`

**Solution:**
- Build local images: `make build-images` (in fabric-x-orderer repo)
- Or use public images: Update `orderer_image` in configuration

#### 3. Port Conflicts

**Problem:** `bind: address already in use`

**Solution:**
- Check for conflicting services: `lsof -i :7050`
- Update port numbers in configuration
- Stop conflicting services

#### 4. Permission Denied

**Problem:** `permission denied` when accessing output directory

**Solution:**
```bash
# Ensure output directory is writable
chmod 755 ./out

# Or run with appropriate permissions
sudo config-builder setup -c network.yaml -o ./out
```

#### 5. Armageddon Not Found

**Problem:** `armageddon not found`

**Solution:**
The tool will automatically try to install armageddon via `go install`. If this fails:

```bash
# Install manually
go install github.com/hyperledger/fabric-x-orderer/cmd/armageddon@v0.0.19

# Or build from source
cd fabric-x-orderer/cmd/armageddon
go build -o $GOPATH/bin/armageddon
```

### Debug Output

For maximum debugging information:

```bash
export KMS_SO_DEBUG=1
export GRPC_HELPER_DEBUG=1
config-builder setup -c network.yaml -o ./out --log-level=debug 2>&1 | tee debug.log
```

This captures all output to both console and `debug.log` file.

## Development

### Building

```bash
# Build for current platform
make build

# Build for all platforms
make build-all

# Install to GOPATH/bin
make install
```

### Testing

```bash
# Run tests
make test

# Run tests with coverage
make test-coverage
```

### Code Quality

```bash
# Format code
make fmt

# Lint code
make lint

# Download dependencies
make deps
```

### Project Structure

```
config-builder/
├── cmd/                    # Command definitions
│   └── root.go            # Root command and subcommands
├── internal/              # Internal packages
│   ├── armageddon/       # Shared config generation
│   ├── bccsp/            # BCCSP configuration
│   ├── compose/          # Docker Compose generation
│   ├── config/           # Configuration loading and types
│   ├── crypto/           # Crypto material generation
│   ├── genesis/          # Genesis block generation
│   ├── setup/            # Setup orchestration
│   └── template/         # Node configuration templates
├── build/                # Build output directory
├── main.go              # Entry point
├── Makefile             # Build automation
└── README.md            # This file
```

### Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature-name`
3. Make your changes
4. Run tests: `make test`
5. Format code: `make fmt`
6. Lint code: `make lint`
7. Commit changes: `git commit -am 'Add feature'`
8. Push to branch: `git push origin feature-name`
9. Submit a pull request

## License

This project is part of the Fabric-X ecosystem. See the main repository for license information.

## Support

For issues, questions, or contributions, please visit the [GitHub repository](https://github.com/Built-by-Sign/fabric-x-tool).
