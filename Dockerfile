# syntax=docker/dockerfile:1

# ---- build ----
FROM golang:1.25-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

# Static binary (matches .goreleaser.yml: CGO off, trimmed, stripped).
# server/static/* is committed and embedded here, so no Node build is needed.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/wingman ./cmd/wingman

# ---- runtime ----
FROM debian:bookworm-slim

# Tooling the agent's shell tool commonly reaches for.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        git \
        curl \
        bash \
        less \
        ripgrep \
        python3 \
        python3-venv \
        python3-pip \
    && rm -rf /var/lib/apt/lists/*

# Common Python libraries in a shared venv on PATH.
ENV VIRTUAL_ENV=/opt/venv
ENV PATH="/opt/venv/bin:${PATH}"
RUN python3 -m venv "$VIRTUAL_ENV" \
    && pip install --no-cache-dir --upgrade pip \
    && pip install --no-cache-dir \
        numpy \
        pandas \
        scipy \
        requests \
        httpx \
        pyyaml \
        rich \
        tabulate \
        beautifulsoup4 \
        lxml \
        openpyxl \
        pillow \
        matplotlib \
        seaborn \
        plotly \
        pytest \
        ipython

# Non-root user.
RUN groupadd --gid 1000 wingman \
    && useradd --uid 1000 --gid 1000 --create-home --shell /bin/bash wingman \
    && mkdir -p /workdir \
    && chown -R wingman:wingman /workdir /opt/venv

COPY --from=build /out/wingman /usr/local/bin/wingman

USER wingman
WORKDIR /workdir

ENV HOME=/home/wingman
ENV SHELL=/bin/bash

# Used by `wingman server --host 0.0.0.0`; publishing remains opt-in.
EXPOSE 9000 9001

# Default: launch the agent TUI. Run interactively, e.g.
#   docker run -it --rm -v "$PWD:/workdir" wingman-agent
ENTRYPOINT ["wingman"]
