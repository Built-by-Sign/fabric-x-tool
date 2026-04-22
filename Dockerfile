# syntax=docker/dockerfile:1

# ---------- Cross-compiling builder ----------
# Pin the builder to the host architecture ($BUILDPLATFORM) and cross-compile
# for $TARGETARCH. This avoids QEMU/Rosetta emulation for the non-native
# architecture when doing multi-platform builds.
FROM --platform=$BUILDPLATFORM golang:1.26 AS builder
ARG BUILDARCH
ARG TARGETARCH
ARG TARGETOS=linux
ENV BUILDARCH=${BUILDARCH} TARGETARCH=${TARGETARCH} TARGETOS=${TARGETOS}

# Install the cross C toolchain only when target arch differs from build arch
# (used by the CGO packages below — fabric-ca and tokengen via miekg/pkcs11).
RUN if [ "${BUILDARCH}" != "${TARGETARCH}" ]; then \
      apt-get update && \
      case "${TARGETARCH}" in \
        amd64) apt-get install -y --no-install-recommends gcc-x86-64-linux-gnu libc6-dev-amd64-cross ;; \
        arm64) apt-get install -y --no-install-recommends gcc-aarch64-linux-gnu libc6-dev-arm64-cross ;; \
      esac && \
      rm -rf /var/lib/apt/lists/*; \
    fi

# xbuild-cgo: for packages that need CGO (pkcs11). Selects cross CC.
# xbuild-pure: for pure-Go packages. CGO=0, no C toolchain needed.
# gobin-dir: echoes where `go install` placed the binary.
#   Go forbids GOBIN when cross-compiling, so we let install use the default
#   path and copy the result into /out afterwards.
RUN printf '%s\n' \
      '#!/bin/sh' \
      'set -e' \
      'if [ "${BUILDARCH}" = "${TARGETARCH}" ]; then CC=gcc' \
      'elif [ "${TARGETARCH}" = "amd64" ]; then CC=x86_64-linux-gnu-gcc' \
      'else CC=aarch64-linux-gnu-gcc' \
      'fi' \
      'export CC CGO_ENABLED=1 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH}"' \
      'exec "$@"' \
      > /usr/local/bin/xbuild-cgo && chmod +x /usr/local/bin/xbuild-cgo && \
    printf '%s\n' \
      '#!/bin/sh' \
      'set -e' \
      'export CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH}"' \
      'exec "$@"' \
      > /usr/local/bin/xbuild-pure && chmod +x /usr/local/bin/xbuild-pure && \
    printf '%s\n' \
      '#!/bin/sh' \
      'if [ "${BUILDARCH}" = "${TARGETARCH}" ]; then echo /go/bin' \
      'else echo /go/bin/${TARGETOS:-linux}_${TARGETARCH}' \
      'fi' \
      > /usr/local/bin/gobin-dir && chmod +x /usr/local/bin/gobin-dir && \
    mkdir -p /out

WORKDIR /build

# ---------- fabric-ca (CGO, pkcs11) ----------
RUN git clone --branch v1.5.16 --depth=1 --single-branch https://github.com/hyperledger/fabric-ca.git
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build,id=gobuild-${TARGETARCH} \
    cd fabric-ca && \
    xbuild-cgo make fabric-ca-client fabric-ca-server -j"$(nproc)" GO_TAGS=pkcs11 && \
    cp bin/fabric-ca-client bin/fabric-ca-server /out/

# ---------- tokengen (CGO, pkcs11) ----------
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build,id=gobuild-${TARGETARCH} \
    xbuild-cgo go install -tags pkcs11 github.com/hyperledger-labs/fabric-token-sdk/cmd/tokengen@v0.10.0 && \
    cp "$(gobin-dir)/tokengen" /out/

# ---------- fabric-x tools + armageddon (pure Go) ----------
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build,id=gobuild-${TARGETARCH} \
    xbuild-pure go install \
        github.com/hyperledger/fabric-x/tools/configtxgen@v0.0.15 \
        github.com/hyperledger/fabric-x/tools/configtxlator@v0.0.15 \
        github.com/hyperledger/fabric-x/tools/cryptogen@v0.0.15 && \
    xbuild-pure go install github.com/hyperledger/fabric-x-orderer/cmd/armageddon@v1.0.0-alpha && \
    GOBIN_DIR="$(gobin-dir)" && \
    cp "$GOBIN_DIR/configtxgen" "$GOBIN_DIR/configtxlator" "$GOBIN_DIR/cryptogen" "$GOBIN_DIR/armageddon" /out/

# ---------- fxconfig (CGO, pulls in fabric-lib-go/bccsp/pkcs11) ----------
# Kept above the config-builder COPY so edits to local source do not
# invalidate this layer.
ARG FABRIC_X_FORK_REF=feat/pkcs11-support
# SHA pinned for reproducibility. The branch name above is kept for human
# readability; bump the SHA when the fork is rebased onto upstream.
ARG FABRIC_X_FORK_SHA=1e5697ed7ef0547b2aa564cdd3baa0dcae4aef6d
RUN git clone --branch ${FABRIC_X_FORK_REF} --single-branch \
    https://github.com/Built-by-Sign/fabric-x.git /build/fabric-x-src \
 && cd /build/fabric-x-src && git checkout ${FABRIC_X_FORK_SHA}
WORKDIR /build/fabric-x-src/tools/fxconfig
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build,id=gobuild-${TARGETARCH} \
    xbuild-cgo go build -ldflags="-s -w" -trimpath -o /out/fxconfig .

# ---------- config-builder (local source, pure Go) ----------
# Dependency manifests copied first so changes to .go files don't bust the
# module-download cache layer.
COPY ./tools/config-builder/go.mod ./tools/config-builder/go.sum /build/config-builder/
WORKDIR /build/config-builder
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY ./tools/config-builder/ /build/config-builder/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build,id=gobuild-${TARGETARCH} \
    xbuild-pure go build -ldflags="-s -w" -trimpath -o /out/config-builder .

# ---------- Minimal runtime image ----------
FROM ubuntu:24.04

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    ca-certificates \
    gettext-base \
    libgrpc++1.51t64 \
    libprotobuf32t64 && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

WORKDIR /app

COPY --from=builder \
    /out/fabric-ca-client \
    /out/fabric-ca-server \
    /out/fxconfig \
    /out/config-builder \
    /out/tokengen \
    /out/configtxgen \
    /out/configtxlator \
    /out/cryptogen \
    /out/armageddon \
    /app/

COPY ./fabric-ca-client-config.yaml.tpl /app/

RUN mkdir -p /app/.fxconfig
COPY ./fxconfig-defaults.yaml /app/.fxconfig/config.yaml

ENV HOME="/app"
ENV PATH="/app:${PATH}"
