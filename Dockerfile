FROM --platform=$BUILDPLATFORM golang:1.25 as builder

WORKDIR /app

# install dependencies
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# branch out to target platform for multi-arch build
ARG TARGETOS TARGETARCH

ARG VERSION
ARG COMMIT

# build the binary for target platform
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH VERSION=$VERSION COMMIT=$COMMIT \
    make build

# Build nsjail from source. This stage is Linux/amd64 only; nsjail is a
# Linux kernel feature and does not cross-compile for other OS targets.
FROM ubuntu:24.04 AS nsjail-builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    autoconf \
    bison \
    ca-certificates \
    flex \
    gcc \
    g++ \
    git \
    libcap-dev \
    libnl-route-3-dev \
    libprotobuf-dev \
    libtool \
    make \
    pkg-config \
    protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*

RUN git clone --depth=1 https://github.com/google/nsjail.git /nsjail-src
WORKDIR /nsjail-src
RUN make -j$(nproc)

# Test-only stage: golang base image (Debian bookworm) + nsjail built from source.
# Go is pre-installed; all nsjail build deps are in Debian main — no universe needed.
# Used by `make test-sandbox`; not referenced by the production image.
FROM golang:1.24 AS test-sandbox
RUN apt-get update && apt-get install -y --no-install-recommends \
    autoconf \
    bison \
    ca-certificates \
    flex \
    libcap-dev \
    libnl-route-3-dev \
    libprotobuf-dev \
    libtool \
    pkg-config \
    protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --depth=1 https://github.com/google/nsjail.git /nsjail-src && \
    make -C /nsjail-src -j$(nproc) && \
    cp /nsjail-src/nsjail /usr/sbin/nsjail

# Test-only stage for local/manual cross-runtime benchmarks. This intentionally
# keeps Go and Python in the image because benchmark-e2e builds the host binary,
# the WASI demo module, and exercises Python file-worker fixtures.
FROM golang:1.25 AS benchmark-e2e
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    make \
    python3 \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /workspace

# Runtime image. Cannot use scratch because nsjail requires shared libraries
# (libcap, libprotobuf, libnl). Image size grows from ~8 MB to ~90-120 MB.
# When --sandbox is not used, shimmy behaves identically to the scratch image.
FROM ubuntu:24.04

RUN apt-get update && apt-get install -y --no-install-recommends \
    libprotobuf32t64 \
    libnl-route-3-200 \
    libcap2 \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=nsjail-builder /nsjail-src/nsjail /usr/sbin/nsjail
COPY --from=builder /app/bin/shimmy /shimmy

ENTRYPOINT ["/shimmy"]
