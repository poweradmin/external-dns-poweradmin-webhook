VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
PROJECT := external-dns-poweradmin-webhook
DOCKER_IMAGE ?= poweradmin/$(PROJECT)

LDFLAGS := -s -w \
	-X main.Version=$(VERSION)

.PHONY: all
all: build

.PHONY: build
build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(PROJECT) ./cmd/webhook

.PHONY: test
test:
	go test -v -race -coverprofile=coverage.out ./...

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: clean
clean:
	rm -f $(PROJECT)
	rm -f coverage.out

.PHONY: docker-build
docker-build:
	docker build -t $(DOCKER_IMAGE):$(VERSION) .
	docker tag $(DOCKER_IMAGE):$(VERSION) $(DOCKER_IMAGE):latest

.PHONY: docker-push
docker-push:
	docker push $(DOCKER_IMAGE):$(VERSION)
	docker push $(DOCKER_IMAGE):latest

.PHONY: run
run: build
	./$(PROJECT)

.PHONY: integration-test
integration-test: ## Run integration tests against PowerAdmin
	@bash scripts/integration-test.sh

.PHONY: deps
deps:
	go mod download
	go mod tidy

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build        - Build the binary"
	@echo "  test         - Run tests with coverage"
	@echo "  lint         - Run golangci-lint"
	@echo "  fmt          - Format code"
	@echo "  vet          - Run go vet"
	@echo "  clean        - Remove build artifacts"
	@echo "  docker-build - Build Docker image"
	@echo "  docker-push  - Push Docker image"
	@echo "  run              - Build and run locally"
	@echo "  integration-test - Run integration tests against PowerAdmin"
	@echo "  deps             - Download and tidy dependencies"
