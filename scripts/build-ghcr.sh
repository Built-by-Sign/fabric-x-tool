#!/bin/bash
set -e

# Configuration
REGISTRY="ghcr.io"
IMAGE_NAME="built-by-sign/fabric-x-tool"
VERSION="${1:-latest}"

echo "🚀 Building and pushing ${REGISTRY}/${IMAGE_NAME}:${VERSION}"
echo "📦 Platforms: linux/amd64, linux/arm64"

# Step 2: Build and push for both platforms
echo ""
echo "🔨 Building Docker image for linux/amd64 and linux/arm64..."
echo "⏱️  This will take approximately 15 minutes..."
docker buildx build \
    --platform linux/amd64,linux/arm64 \
    --tag ${REGISTRY}/${IMAGE_NAME}:${VERSION} \
    --tag ${REGISTRY}/${IMAGE_NAME}:latest \
    .

echo ""
echo "✅ Successfully built"
