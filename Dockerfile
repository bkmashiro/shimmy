FROM --platform=$BUILDPLATFORM golang:1.22 AS builder

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

FROM scratch

# add binary to empty scratch image
COPY --from=builder /app/bin/shimmy /shimmy
