BIN := bin
VERSION ?= dev
BUILD_TIME ?= unknown
LDFLAGS := -s -w
LINT_IMAGE := golangci/golangci-lint:v2.12.2-alpine
export CGO_ENABLED=0

.PHONY: build test test-race e2e fmt fmt-check vet lint preflight clean

build:
	go build -ldflags "$(LDFLAGS) -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)" -o $(BIN)/tunlease ./cmd/cli
	go build -ldflags "$(LDFLAGS)" -o $(BIN)/tunlease-gateway ./cmd/gateway
	go build -ldflags "$(LDFLAGS)" -o $(BIN)/tunlease-sidecar ./cmd/sidecar

test:
	go test ./... -v

test-race:
	go test -race ./...

e2e:
	./scripts/e2e-compose.sh

fmt:
	gofmt -w .

fmt-check:
	@files="$$(gofmt -l .)"; if [ -n "$$files" ]; then echo "gofmt required:"; echo "$$files"; exit 1; fi

vet:
	go vet ./...

lint:
	docker run --rm -v "$(CURDIR):/app" -w /app $(LINT_IMAGE) golangci-lint run ./...

# Local equivalent of the CI quality gate.
preflight: fmt-check
	go build ./...
	go vet ./...
	go test -race ./...
	$(MAKE) lint
	@echo "preflight OK"

clean:
	rm -rf $(BIN)
