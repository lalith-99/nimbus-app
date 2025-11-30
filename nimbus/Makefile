.PHONY: help build run test clean deps test-cover test-quick lint dev

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
	go build -o bin/gateway ./cmd/gateway

run-gateway: ## Run gateway locally
	go run ./cmd/gateway/main.go

test: ## Run tests
	go test -v ./...

clean: ## Clean build artifacts
	rm -rf bin/
