FROM golang:1.26.4-alpine@sha256:7a3e50096189ad57c9f9f865e7e4aa8585ed1585248513dc5cda498e2f41812c AS builder

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
FROM gcr.io/distroless/static-debian12:nonroot@sha256:d093aa3e30dbadd3efe1310db061a14da60299baff8450a17fe0ccc514a16639
USER 20000:20000
COPY --chmod=555 --from=builder /app/external-dns-poweradmin-webhook /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/external-dns-poweradmin-webhook"]
