FROM node:24-bookworm AS node

FROM rust:1.99.0-bookworm AS rust
RUN rustup target add wasm32-unknown-unknown

FROM golang:1.26.3-bookworm AS build

COPY --from=node /usr/local /usr/local
COPY --from=rust /usr/local/rustup /usr/local/rustup
COPY --from=rust /usr/local/cargo /usr/local/cargo
ENV RUSTUP_HOME=/usr/local/rustup \
    CARGO_HOME=/usr/local/cargo \
    PATH=/usr/local/cargo/bin:$PATH

RUN apt-get update \
    && apt-get install --no-install-recommends --yes brotli zstd \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

COPY go.mod go.sum ./
COPY package.json package-lock.json ./
RUN npm ci --ignore-scripts

RUN go mod download

COPY . ./

ARG ANUBIS_VERSION=devel
RUN npm run assets \
    && CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/TecharoHQ/anubis.Version=${ANUBIS_VERSION}" \
      -o /out/anubis ./cmd/anubis

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build --chown=1000:1000 /out/anubis /usr/local/bin/anubis

USER 1000:1000

ENTRYPOINT ["/usr/local/bin/anubis"]
