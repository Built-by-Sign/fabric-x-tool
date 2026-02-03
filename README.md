# fabric-x-tool

## Overview

This project provides a Docker image with Hyperledger Fabric tools including:

- fabric-ca-client and fabric-ca-server (with PKCS11 support)
- tokengen
- fxconfig (with PKCS11 support)
- cryptogen
- configtxgen
- configtxlator
- cryptogen

## Docker Image

The Docker image is automatically built and published to GitHub Container Registry (GHCR) when a version tag is pushed.

### Pull the image

```bash
docker pull ghcr.io/signstack/fabric-x-tool:latest
```

### Available tags

- `latest` - Latest released version
- `1.0.0` - Specific version (e.g., v1.0.0)

### Supported platforms

- linux/amd64
- linux/arm64

### Publishing a new version

To publish a new version, create and push a git tag with the version number:

```bash
# Create a new version tag (e.g., v1.0.0)
git tag v1.0.0

# Push the tag to GitHub
git push origin v1.0.0
```

This will automatically trigger the GitHub Actions workflow to build and publish the Docker image to GHCR with the corresponding version tags.

### Manual build

You can also manually trigger a build from the GitHub Actions UI:

1. Go to the repository on GitHub
2. Click on the "Actions" tab
3. Select "Publish to GHCR" workflow from the left sidebar
4. Click "Run workflow" button on the right
5. Select the branch and click "Run workflow"

Note: Manual builds will use the branch name as the tag (not a version number), so it's recommended to use version tags for releases.

## Building locally

```bash
# Build the image
make build
```

## CI/CD

The project uses GitHub Actions to automatically build and publish Docker images to GHCR. The workflow is triggered on:

- Version tags (v\*) - Automatically builds and publishes when you push a version tag
- Manual workflow dispatch - Can be triggered manually from GitHub Actions UI

No additional configuration is required - the workflow uses the built-in `GITHUB_TOKEN` for authentication.
