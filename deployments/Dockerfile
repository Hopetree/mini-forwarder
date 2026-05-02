# ── Build Stage ──────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .

# Inject build-time variables at docker build time.
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build \
    -ldflags="-w -s \
      -X main.Version=${VERSION} \
      -X main.GitCommit=${GIT_COMMIT} \
      -X main.BuildDate=${BUILD_DATE}" \
    -o /bin/mini-forwarder ./cmd/mini-forwarder

# ── Runtime Stage ───────────────────────────────────────────────────────
FROM scratch

# Copy CA certificates and timezone data from builder.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy the binary.
COPY --from=builder /bin/mini-forwarder /bin/mini-forwarder

# Copy default config.
COPY configs/forwarder.yaml /etc/mini-forwarder/forwarder.yaml

EXPOSE 3000 6379 3306

ENTRYPOINT ["/bin/mini-forwarder"]
CMD ["-config", "/etc/mini-forwarder/forwarder.yaml"]
