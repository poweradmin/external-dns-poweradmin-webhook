# external-dns-poweradmin-webhook

[![CI](https://github.com/poweradmin/external-dns-poweradmin-webhook/actions/workflows/test.yaml/badge.svg)](https://github.com/poweradmin/external-dns-poweradmin-webhook/actions/workflows/test.yaml)
[![Lint](https://github.com/poweradmin/external-dns-poweradmin-webhook/actions/workflows/lint.yaml/badge.svg)](https://github.com/poweradmin/external-dns-poweradmin-webhook/actions/workflows/lint.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/poweradmin/external-dns-poweradmin-webhook)](https://goreportcard.com/report/github.com/poweradmin/external-dns-poweradmin-webhook)
[![GitHub Release](https://img.shields.io/github/v/release/poweradmin/external-dns-poweradmin-webhook)](https://github.com/poweradmin/external-dns-poweradmin-webhook/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A webhook provider for [ExternalDNS](https://github.com/kubernetes-sigs/external-dns) that integrates with [PowerAdmin](https://github.com/poweradmin/poweradmin) DNS management system.

## Features

- Full integration with PowerAdmin API (v1 and v2 supported)
- Supports A, AAAA, CNAME, TXT, MX, NS, SRV, PTR, and CAA record types
- Domain filtering support
- Dry-run mode for testing
- Prometheus metrics endpoint
- Health check endpoints for Kubernetes probes

## Requirements

- PowerAdmin with API enabled (v1 or v2)
- API key with appropriate permissions
- Kubernetes cluster (for deployment)
- ExternalDNS v0.20.0 or later

## Configuration

The webhook is configured via environment variables:

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `POWERADMIN_URL` | Yes | - | Base URL of your PowerAdmin instance |
| `POWERADMIN_API_KEY` | Yes | - | API key for authentication |
| `POWERADMIN_API_VERSION` | No | `v2` | API version to use (`v1` or `v2`) |
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

Pre-built images are available from:
- GitHub Container Registry: `ghcr.io/poweradmin/external-dns-poweradmin-webhook`
- Docker Hub: `docker.io/poweradmin/external-dns-poweradmin-webhook`

```bash
# Pull from GitHub Container Registry
docker pull ghcr.io/poweradmin/external-dns-poweradmin-webhook:latest

# Or pull from Docker Hub
docker pull poweradmin/external-dns-poweradmin-webhook:latest

# Build locally
docker build -t poweradmin/external-dns-poweradmin-webhook:latest .
```

## Deployment

### Helm

The recommended way to deploy is using the [ExternalDNS Helm chart](https://github.com/kubernetes-sigs/external-dns/tree/master/charts/external-dns).

Add the ExternalDNS Helm repository:

```shell
helm repo add external-dns https://kubernetes-sigs.github.io/external-dns/
helm repo update
```

Create a secret with your PowerAdmin credentials:

```shell
kubectl create namespace external-dns

kubectl create secret generic poweradmin-credentials \
  --from-literal=POWERADMIN_URL=https://poweradmin.example.com \
  --from-literal=POWERADMIN_API_KEY=your-api-key \
  -n external-dns
```

Create a Helm values file `external-dns-poweradmin-values.yaml`:

```yaml
namespace: external-dns
policy: sync
sources:
  - service
  - ingress

provider:
  name: webhook
  webhook:
    image:
      repository: ghcr.io/poweradmin/external-dns-poweradmin-webhook
      tag: latest  # replace with a specific version
    env:
      - name: POWERADMIN_URL
        valueFrom:
          secretKeyRef:
            name: poweradmin-credentials
            key: POWERADMIN_URL
      - name: POWERADMIN_API_KEY
        valueFrom:
          secretKeyRef:
            name: poweradmin-credentials
            key: POWERADMIN_API_KEY
      - name: POWERADMIN_API_VERSION
        value: "v2"
      - name: DOMAIN_FILTER
        value: "example.com"  # replace with your domain(s)
      - name: SERVER_HOST
        value: "localhost"
    livenessProbe:
      httpGet:
        path: /healthz
        port: http-webhook
      initialDelaySeconds: 10
      timeoutSeconds: 5
    readinessProbe:
      httpGet:
        path: /readyz
        port: http-webhook
      initialDelaySeconds: 10
      timeoutSeconds: 5
```

Install ExternalDNS with Helm:

```shell
helm install external-dns-poweradmin external-dns/external-dns \
  -f external-dns-poweradmin-values.yaml \
  -n external-dns
```

### Kubernetes (manual)

Alternatively, you can deploy using raw manifests. The webhook runs as a sidecar container alongside ExternalDNS.

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

The webhook supports both PowerAdmin API v1 and v2. By default, v2 is used. To use v1, set `POWERADMIN_API_VERSION=v1`.

Ensure your PowerAdmin instance has:

1. API enabled (v1 or v2)
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
│       ├── provider.go       # ExternalDNS provider implementation
│       └── provider_test.go  # Provider tests
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
