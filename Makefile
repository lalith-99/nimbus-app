.PHONY: help build run test clean deps test-cover test-quick lint dev docker-build docker-push validate ci-local

# Configuration
REGISTRY ?= 
REPOSITORY ?= nimbus-prod
IMAGE_TAG ?= latest
GO_VERSION := 1.23

test-cover: ## Run tests with coverage report
	go test ./... -cover -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

test-quick: ## Run tests (quick, no verbose)
	go test ./...

lint: ## Run linter
	golangci-lint run

dev: ## Build and run gateway
	go run ./cmd/gateway/main.go

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

deps: ## Download dependencies
	go mod download
	go mod tidy

build: ## Build all binaries
	CGO_ENABLED=0 GOOS=linux go build -o bin/gateway ./cmd/gateway

run-gateway: ## Run gateway locally
	go run ./cmd/gateway/main.go

test: ## Run tests
	go test -v ./...

clean: ## Clean build artifacts
	rm -rf bin/

# Docker targets
docker-build: ## Build Docker image
	docker build -t $(REPOSITORY):$(IMAGE_TAG) .
	@echo "✅ Docker image built: $(REPOSITORY):$(IMAGE_TAG)"

docker-push: docker-build ## Push Docker image to registry
	@if [ -z "$(REGISTRY)" ]; then \
		echo "❌ REGISTRY not set. Usage: make docker-push REGISTRY=your-registry"; \
		exit 1; \
	fi
	docker tag $(REPOSITORY):$(IMAGE_TAG) $(REGISTRY)/$(REPOSITORY):$(IMAGE_TAG)
	docker push $(REGISTRY)/$(REPOSITORY):$(IMAGE_TAG)
	docker tag $(REPOSITORY):$(IMAGE_TAG) $(REGISTRY)/$(REPOSITORY):latest
	docker push $(REGISTRY)/$(REPOSITORY):latest
	@echo "✅ Image pushed to $(REGISTRY)/$(REPOSITORY)"

# CI automation
ci-local: deps lint test build ## Run full CI pipeline locally
	@echo "✅ Local CI pipeline passed"

ci-docker: ci-local docker-build ## Run CI + Docker build locally
	@echo "✅ CI + Docker pipeline complete"

validate: ## Validate project structure and dependencies
	@echo "🔍 Validating project..."
	@go mod verify
	@go vet ./...
	@echo "✅ Project validation passed"

all: validate ci-local ## Run full validation and CI
