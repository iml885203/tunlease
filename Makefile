BIN := bin
VERSION ?= dev
BUILD_TIME ?= unknown
LDFLAGS := -s -w
GOLANGCI_LINT := github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
export CGO_ENABLED=0

.PHONY: build test test-race e2e fmt fmt-check vet lint hooks preflight clean

build:
	go build -ldflags "$(LDFLAGS) -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)" -o $(BIN)/tul ./cmd/tunlease

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
	go run $(GOLANGCI_LINT) run ./...

hooks:
	git config core.hooksPath .githooks
	@echo "Git hooks enabled from .githooks"

# Local equivalent of the CI quality gate.
preflight: fmt-check
	go build ./...
	go vet ./...
	go test -race ./...
	$(MAKE) lint
	@echo "preflight OK"

clean:
	rm -rf $(BIN)
