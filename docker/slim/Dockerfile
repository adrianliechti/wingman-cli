# syntax=docker/dockerfile:1

# Build on the native builder architecture and cross-compile the static binary
# so buildx can publish amd64 and arm64 from either kind of host.
FROM --platform=$BUILDPLATFORM golang:1-bookworm AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w" -o /out/wingman ./cmd/wingman

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git openssh-client \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/wingman /usr/local/bin/wingman

WORKDIR /workspace

ENV SHELL=/bin/bash

EXPOSE 8080

# No account is created and no image-level identity is selected. The Docker or
# Kubernetes runtime chooses the UID/GID for each worker.
ENTRYPOINT ["/usr/local/bin/wingman"]
CMD ["acp", "wingman", "--listen", ":8080"]
