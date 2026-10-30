FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS builder

WORKDIR /app

# Install build dependencies
RUN apk --no-cache add make git

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.Version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o external-dns-poweradmin-webhook ./cmd/webhook

# Runtime stage
FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
USER 20000:20000
COPY --chmod=555 --from=builder /app/external-dns-poweradmin-webhook /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/external-dns-poweradmin-webhook"]
