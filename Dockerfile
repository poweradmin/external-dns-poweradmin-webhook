FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk --no-cache add make git ca-certificates

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.Version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o external-dns-poweradmin-webhook ./cmd/webhook

# Runtime stage
FROM alpine:3.23

RUN apk --no-cache add ca-certificates

# Create non-root user
RUN adduser -D -u 1000 webhook
USER webhook

COPY --from=builder /app/external-dns-poweradmin-webhook /usr/local/bin/

ENTRYPOINT ["external-dns-poweradmin-webhook"]
