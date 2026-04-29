# Build environment for bink CLI with all C dependencies
ARG FEDORA_VERSION=43
FROM registry.fedoraproject.org/fedora:${FEDORA_VERSION}

# Install Go and required C libraries for Podman bindings
RUN dnf install -y \
    golang \
    git \
    gpgme-devel \
    btrfs-progs-devel \
    device-mapper-devel \
    && dnf clean all

WORKDIR /build

# Copy go.mod and go.sum first to cache dependencies separately
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy source and build
COPY . /src
WORKDIR /output
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd /src && CGO_ENABLED=1 go build -o /output/bink ./cmd/bink
