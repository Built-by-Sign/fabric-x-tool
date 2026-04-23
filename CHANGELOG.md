# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]


## [0.0.8] - 2026-04-20

This release retags `v0.0.8` on top of the continued config-builder and image
work on `feat/fabric-x-upgrade-v0.1.9`. The previous `v0.0.8` tag (`537ecc3`)
is force-moved; downstream images using `v0.0.8` must be rebuilt.

### Added
- Introduced `config-builder` generator package with orderer / committer /
  armageddon / genesis / fabric-ca templates derived from the official
  `LF-Decentralized-Trust-labs/fabric-x-ansible-collection` non-Kubernetes
  roles. All rendered YAML files are vendored under `templates/`.
- Committer sidecar now provisions its own peer MSP under a peer
  organization (`/config/msp`) instead of reusing a channel admin identity,
  matching the Ansible role.
- Committer supports an external database config (`committer.database`) for
  PostgreSQL or YugabyteDB, including TLS CA wiring, Yugabyte load-balance
  defaults, and table pre-split tablet defaults.
- Local PostgreSQL committer DB gains server-side TLS when global
  `tls.enabled: true`, mirroring the Ansible postgres role
  (`server.key` / `server.crt` / `ca.crt` under `config/tls`,
  `ssl=on` startup args, DB CA wired into validator / query-service).
- Configurable `monitoring_port` per node; default is `service port + 10`.
- Configurable PostgreSQL image (`docker.postgres_image`).
- TLS configuration block (`tls.enabled`, `tls.client_auth_required`).
- KMS integration hardening: organization-level `kms_token_label` /
  `kms_user_pin`, per-node `user_pin` override, validation paths, and
  Fabric CA client BCCSP config rendered from the vendored template.
- BuildKit cache mounts in the Docker image for faster multi-platform builds.
- System monitoring hooks (metrics ports, Prometheus-friendly defaults).

### Changed
- **Breaking**: orderer `type` in the network YAML is now `consensus`
  instead of `consenter`, matching the Ansible `orderer_component_type`
  vocabulary. The generator no longer aliases the old value; existing YAML
  must be updated.
- `fxconfig` is now built from the `Built-by-Sign/fabric-x` fork (PKCS#11
  support) inside the Dockerfile rather than vendored locally. Builder base
  bumped to `golang:1.26`.
- Module path and network name updated under `Built-by-Sign/fabric-x-tool`.
- Docker image registry name standardized to lowercase.
- Dependencies refreshed for compatibility with the v0.0.12 orderer /
  v0.1.9 committer ecosystem.

### Fixed
- Guarded committer monitoring template against zero ports.
- Reordered Dockerfile `COPY` layers for better cache reuse.

### Infrastructure
- Config generation is aligned with the official Fabric-X Ansible collection
  (non-Kubernetes flow). Kubernetes manifests, monitoring/exporter roles,
  local YugabyteDB cluster orchestration, and the Ansible container
  lifecycle/wait behavior remain out of scope.

## [0.0.3] - 2026-02-04

### Added
- Added config-builder tool for Fabric-X network configuration management
  - Comprehensive tool for generating Hyperledger Fabric network configurations
  - Support for crypto material generation using Fabric CA
  - Docker Compose file generation for network deployment
  - Genesis block and channel configuration generation
  - Armageddon script generation for network cleanup
  - Extensive configuration options via YAML files
- Integrated config-builder tool into Docker image

## [0.0.2] - 2026-02-03

### Changed
- Upgraded Go to version 1.25.6
- Disabled auto-publish workflow for better control over releases

### Added
- Added armageddon tool to the Docker image
- Added manual build script (`scripts/build-ghcr.sh`) for GHCR publishing
- Added support for manual workflow dispatch in GitHub Actions

## [0.0.1] - 2026-02-03

### Added
- Initial project setup
- Docker build setup for Hyperledger Fabric tools including:
  - fabric-ca-client and fabric-ca-server (with PKCS11 support)
  - tokengen
  - fxconfig (with PKCS11 support)
  - cryptogen
  - configtxgen
  - configtxlator
- fxconfig CLI tool for namespace management operations with commands:
  - `namespace create` - Create new namespaces
  - `namespace list` - List existing namespaces
  - `namespace update` - Update namespace configurations
- GitHub Actions workflow for publishing Docker images to GHCR
- Multi-platform support (linux/amd64, linux/arm64)
- Makefile for local builds
- Comprehensive README with usage instructions

### Infrastructure
- Set up GitHub Container Registry (GHCR) integration
- Configured automated Docker image publishing on version tags
- Added Dockerfile with multi-stage build process

[Unreleased]: https://github.com/Built-by-Sign/fabric-x-tool/compare/v0.0.8...HEAD
[0.0.8]: https://github.com/Built-by-Sign/fabric-x-tool/compare/v0.0.3...v0.0.8
[0.0.3]: https://github.com/Built-by-Sign/fabric-x-tool/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/Built-by-Sign/fabric-x-tool/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/Built-by-Sign/fabric-x-tool/releases/tag/v0.0.1
