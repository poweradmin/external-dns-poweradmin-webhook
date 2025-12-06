# external-dns-poweradmin-webhook

A webhook provider for [ExternalDNS](https://github.com/kubernetes-sigs/external-dns) that integrates with [PowerAdmin](https://github.com/poweradmin/poweradmin) DNS management system using the v2 API.

## Features

- Full integration with PowerAdmin v2 API
- Supports A, AAAA, CNAME, TXT, MX, NS, SRV, PTR, and CAA record types
- Domain filtering support
- Dry-run mode for testing
- Prometheus metrics endpoint
- Health check endpoints for Kubernetes probes

## Requirements

- PowerAdmin with v2 API enabled
- API key with appropriate permissions
- Kubernetes cluster (for deployment)
- ExternalDNS v0.14.0 or later

## Configuration

The webhook is configured via environment variables:

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `POWERADMIN_URL` | Yes | - | Base URL of your PowerAdmin instance |
| `POWERADMIN_API_KEY` | Yes | - | API key for authentication |
| `DOMAIN_FILTER` | No | - | Comma-separated list of domains to manage |
| `EXCLUDE_DOMAIN_FILTER` | No | - | Comma-separated list of domains to exclude |
| `REGEXP_DOMAIN_FILTER` | No | - | Regex pattern for domain filtering |
| `SERVER_HOST` | No | `localhost` | Webhook server bind address |
| `SERVER_PORT` | No | `8888` | Webhook server port |
| `METRICS_HOST` | No | `0.0.0.0` | Metrics/health server bind address |
| `METRICS_PORT` | No | `8080` | Metrics/health server port |
| `SERVER_READ_TIMEOUT` | No | `5s` | HTTP server read timeout |
| `SERVER_WRITE_TIMEOUT` | No | `10s` | HTTP server write timeout |
| `LOG_LEVEL` | No | `info` | Log level (debug, info, warn, error) |
| `LOG_FORMAT` | No | `text` | Log format (text, json) |
| `DRY_RUN` | No | `false` | Enable dry-run mode |

## Building

### From Source

```bash
# Build the binary
make build

# Run tests
make test

# Build Docker image
make docker-build
```

### Docker

```bash
docker build -t poweradmin/external-dns-poweradmin-webhook:latest .
```

## Deployment

### Kubernetes

The webhook is designed to run as a sidecar container alongside ExternalDNS.

1. Create a namespace:
```bash
kubectl create namespace external-dns
```

2. Create the secret with your PowerAdmin credentials:
```bash
kubectl create secret generic poweradmin-credentials \
  --from-literal=POWERADMIN_URL=https://poweradmin.example.com \
  --from-literal=POWERADMIN_API_KEY=your-api-key \
  -n external-dns
```

3. Apply the deployment:
```bash
kubectl apply -f deploy/kubernetes/deployment.yaml
```

### Local Testing

```bash
export POWERADMIN_URL=https://poweradmin.example.com
export POWERADMIN_API_KEY=your-api-key
export DOMAIN_FILTER=example.com

./external-dns-poweradmin-webhook
```

## API Endpoints

### Webhook Endpoints (localhost:8888)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | Negotiate capabilities and return domain filter |
| `/records` | GET | Get current DNS records |
| `/records` | POST | Apply DNS record changes |
| `/adjustendpoints` | POST | Adjust endpoints before applying |

### Health/Metrics Endpoints (0.0.0.0:8080)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/healthz` | GET | Liveness probe |
| `/readyz` | GET | Readiness probe |
| `/metrics` | GET | Prometheus metrics |

## PowerAdmin API Requirements

The webhook uses the PowerAdmin v2 API. Ensure your PowerAdmin instance has:

1. API v2 enabled
2. An API key created with permissions to:
   - List zones
   - List records
   - Create records
   - Update records
   - Delete records

## Development

### Project Structure

```
.
├── cmd/
│   └── webhook/
│       └── main.go           # Application entry point
├── internal/
│   └── poweradmin/
│       ├── client.go         # PowerAdmin API client
│       └── provider.go       # ExternalDNS provider implementation
├── deploy/
│   └── kubernetes/
│       └── deployment.yaml   # Kubernetes deployment manifest
├── Dockerfile
├── Makefile
├── go.mod
└── README.md
```

### Running Tests

```bash
make test
```

### Linting

```bash
make lint
```

## License

MIT License - see [LICENSE](LICENSE) for details.
