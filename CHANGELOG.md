# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/signstack/fabric-x-tool/compare/v0.0.2...HEAD
[0.0.2]: https://github.com/signstack/fabric-x-tool/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/signstack/fabric-x-tool/releases/tag/v0.0.1
