FROM node:24-bookworm AS node

FROM golang:1.26.3-bookworm AS build

COPY --from=node /usr/local /usr/local

RUN apt-get update \
    && apt-get install --no-install-recommends --yes brotli zstd \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

COPY go.mod go.sum ./
COPY package.json package-lock.json ./
RUN npm ci

RUN go mod download

COPY . ./

ARG ANUBIS_VERSION=devel
RUN npm run assets \
    && CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/TecharoHQ/anubis.Version=${ANUBIS_VERSION}" \
      -o /out/anubis ./cmd/anubis

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build --chown=1000:1000 /out/anubis /anubis

USER 1000:1000

ENTRYPOINT ["/anubis"]
