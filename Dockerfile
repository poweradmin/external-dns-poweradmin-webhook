FROM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS builder

WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Version comes from the caller (make docker-build passes it): .dockerignore
# drops tracked files, so git describe inside the build always reads -dirty.
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.Version=${VERSION}" -o external-dns-poweradmin-webhook ./cmd/webhook

# Runtime stage
FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
USER 20000:20000
COPY --chmod=555 --from=builder /app/external-dns-poweradmin-webhook /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/external-dns-poweradmin-webhook"]
