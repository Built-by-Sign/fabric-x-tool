# ---------- build fabric-ca -------------
FROM golang:1.25.6 AS builder
WORKDIR /build

# clone fabric-ca repo
RUN git clone --branch v1.5.16 --depth=1 --single-branch https://github.com/hyperledger/fabric-ca.git

# copy local fxconfig tool
COPY ./tools/fxconfig/ ./fxconfig
# copy local config-builder tool
COPY ./tools/config-builder/ ./config-builder

# build fabric-ca with parallel compilation
RUN cd fabric-ca && \
    make fabric-ca-client GO_TAGS=pkcs11 -j$(nproc) && \
    make fabric-ca-server GO_TAGS=pkcs11 -j$(nproc)

# build tokengen
RUN go install -tags "pkcs11" github.com/hyperledger-labs/fabric-token-sdk/cmd/tokengen@v0.8.0
# build configtxgen configtxlator cryptogen
RUN go install \
    github.com/hyperledger/fabric-x/tools/configtxgen@v0.0.8 \
    github.com/hyperledger/fabric-x/tools/configtxlator@v0.0.8 \
    github.com/hyperledger/fabric-x/tools/cryptogen@v0.0.8
RUN go install github.com/hyperledger/fabric-x-orderer/cmd/armageddon@v0.0.21

# build fxconfig
WORKDIR /build/fxconfig
RUN go mod download
RUN go build -ldflags="-s -w" -trimpath -o fxconfig .

# build config-builder
WORKDIR /build/config-builder
RUN go mod download
RUN go build -ldflags="-s -w" -trimpath -o config-builder .

# --------- Minimal runtime image --------------
FROM debian:12-slim

# Install runtime dependencies
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    ca-certificates \
    gettext-base \
    libgrpc-dev \
    libgrpc++-dev \
    libprotobuf-dev \
    libprotobuf-c-dev && \
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
