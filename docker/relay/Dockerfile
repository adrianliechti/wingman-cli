# syntax=docker/dockerfile:1

# Rendezvous relay for remote control: `wingman relay`.
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

FROM scratch

COPY --from=build /out/wingman /usr/local/bin/wingman

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/wingman", "relay"]
