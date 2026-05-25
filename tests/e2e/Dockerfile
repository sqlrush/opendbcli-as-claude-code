# OpenDB Autopilot — E2E Test Container
FROM golang:1.26-bookworm AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -tags full -o /opendb ./cmd/opendb/

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl iproute2 procps && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /opendb /usr/local/bin/opendb
RUN mkdir -p /root/.opendb

ENTRYPOINT []
CMD ["opendb"]
