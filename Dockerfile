# syntax=docker/dockerfile:1
# ---------- build fabric-ca -------------
FROM golang:1.26 AS builder
WORKDIR /build

# clone fabric-ca repo
RUN git clone --branch v1.5.16 --depth=1 --single-branch https://github.com/hyperledger/fabric-ca.git

# build fabric-ca with parallel compilation
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd fabric-ca && \
    make fabric-ca-client GO_TAGS=pkcs11 -j$(nproc) && \
    make fabric-ca-server GO_TAGS=pkcs11 -j$(nproc)

# build remote dependencies (these rarely change)
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go install -tags "pkcs11" github.com/hyperledger-labs/fabric-token-sdk/cmd/tokengen@v0.10.0
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go install \
    github.com/hyperledger/fabric-x/tools/configtxgen@v0.0.12 \
    github.com/hyperledger/fabric-x/tools/configtxlator@v0.0.12 \
    github.com/hyperledger/fabric-x/tools/cryptogen@v0.0.12
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go install github.com/hyperledger/fabric-x-orderer/cmd/armageddon@v0.0.24

# Build fxconfig from our Built-by-Sign/fabric-x fork (PKCS#11 / HSM support
# until upstream merges the patch). Keep this above the config-builder COPY so
# edits to the local config-builder source do not invalidate this layer.
ARG FABRIC_X_FORK_REF=feat/pkcs11-support
RUN git clone --branch ${FABRIC_X_FORK_REF} --depth=1 --single-branch \
    https://github.com/Built-by-Sign/fabric-x.git /build/fabric-x-src
WORKDIR /build/fabric-x-src/tools/fxconfig
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags="-s -w" -trimpath -o /build/fxconfig/fxconfig .

# copy local source AFTER remote builds so source changes don't invalidate above layers
COPY ./tools/config-builder/ /build/config-builder

# build config-builder
WORKDIR /build/config-builder
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download && \
    go build -ldflags="-s -w" -trimpath -o config-builder .

# --------- Minimal runtime image --------------
FROM debian:12-slim

# Install ONLY runtime dependencies (removed all -dev packages)
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    ca-certificates \
    gettext-base \
    libgrpc++1.51 \
    libprotobuf32 && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

WORKDIR /app

# copy compiled binaries only
COPY --from=builder /build/fabric-ca/bin/fabric-ca-client \
    /build/fabric-ca/bin/fabric-ca-server \
    /build/fxconfig/fxconfig \
    /build/config-builder/config-builder \
    /go/bin/tokengen \
    /go/bin/configtxgen \
    /go/bin/configtxlator \
    /go/bin/cryptogen \
    /go/bin/armageddon \
    /app/

# copy configuration template
COPY ./fabric-ca-client-config.yaml.tpl /app/

# install default fxconfig config (loaded from $HOME/.fxconfig/config.yaml)
RUN mkdir -p /app/.fxconfig
COPY ./fxconfig-defaults.yaml /app/.fxconfig/config.yaml

ENV HOME="/app"
ENV PATH="/app:${PATH}"
