BINARY     := autoscaler-cloudscale
MODULE     := github.com/kubeterm-sh/autoscaler-cloudscale
IMAGE      := ghcr.io/kubeterm-sh/autoscaler-cloudscale
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(MODULE)/pkg/version.Version=$(VERSION) \
	-X $(MODULE)/pkg/version.Commit=$(COMMIT) \
	-X $(MODULE)/pkg/version.BuildDate=$(BUILD_DATE)

GOFLAGS := -trimpath

UPSTREAM_PROTO := https://raw.githubusercontent.com/kubernetes/autoscaler/master/cluster-autoscaler/cloudprovider/externalgrpc/protos/externalgrpc.proto
GO_PACKAGE     := github.com/kubeterm-sh/autoscaler-cloudscale/proto

.PHONY: all build build-linux clean test test-cover lint proto proto-sync docker docker-push help

all: build

## build: Build the binary
build:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/autoscaler-cloudscale/

## build-linux: Cross-compile for linux/amd64
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/autoscaler-cloudscale/

## test: Run tests
test:
	go test -v -race ./...

## test-cover: Run tests with coverage
test-cover:
	go test -v -race -coverprofile=coverage.out ./...

## lint: Run linters
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "Installing golangci-lint..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $$(go env GOPATH)/bin; \
	}
	golangci-lint run ./...

## proto: Regenerate protobuf Go code
proto:
	protoc \
		--go_out=proto --go_opt=paths=source_relative \
		--go-grpc_out=proto --go-grpc_opt=paths=source_relative \
		-I proto \
		proto/externalgrpc.proto

## proto-sync: Fetch latest proto from upstream and regenerate
proto-sync:
	@echo "Fetching latest proto from upstream..."
	curl -sSfL $(UPSTREAM_PROTO) -o proto/externalgrpc.proto
	sed -i 's|option go_package = .*|option go_package = "$(GO_PACKAGE)";|' proto/externalgrpc.proto
	$(MAKE) proto
	@echo "Proto synced and regenerated."

## clean: Remove build artifacts
clean:
	rm -f $(BINARY)
	go clean ./...

## docker: Build Docker image
docker:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		-t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

## docker-push: Push Docker image
docker-push: docker
	docker push $(IMAGE):$(VERSION)
	docker push $(IMAGE):latest

## help: Show this help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | column -t -s ':'
