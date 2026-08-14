FROM golang:1.27rc2-alpine@sha256:dcbb18cc5fa1082364dc6aa95224b6b55429d09cbb9631a053d8064c1c367300 AS builder

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
FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35
USER 20000:20000
COPY --chmod=555 --from=builder /app/external-dns-poweradmin-webhook /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/external-dns-poweradmin-webhook"]
