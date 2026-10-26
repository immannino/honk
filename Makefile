GO_VERSION := $(shell awk '/^go /{print $$2}' go.mod)
BIN        := honk
PKGS       := ./...

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: test
test: ## Run tests with the race detector
	go test -race $(PKGS)

.PHONY: test-v
test-v: ## Run tests verbosely, uncached
	go test -race -count=1 -v $(PKGS)

.PHONY: test-ci
test-ci: ## Run tests hidden from your personal git config (closer to CI)
	@tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && \
	HOME="$$tmp" XDG_CONFIG_HOME="$$tmp" GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 \
		go test -race -count=1 $(PKGS)

.PHONY: test-linux
test-linux: ## Run tests in a Linux container as a user with no git identity, like the CI runner
	docker run --rm \
		--user "$$(id -u):$$(id -g)" \
		-e HOME=/tmp -e GOCACHE=/tmp/go-cache -e GOFLAGS=-buildvcs=false \
		-v "$$(go env GOMODCACHE)":/go/pkg/mod \
		-v "$(CURDIR)":/src -w /src \
		golang:$(GO_VERSION) go test -race -count=1 $(PKGS)

.PHONY: cover
cover: ## Run tests and open an HTML coverage report
	go test -race -coverprofile=coverage.out $(PKGS)
	go tool cover -html=coverage.out

.PHONY: fmt
fmt: ## Format code
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file needs formatting
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "needs gofmt:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Run go vet
	go vet $(PKGS)

.PHONY: check
check: fmt-check vet test ## Everything CI runs: format check, vet, tests

.PHONY: build
build: ## Build ./honk
	go build -o $(BIN) .

.PHONY: install
install: ## Install honk to $(go env GOPATH)/bin
	go install .

.PHONY: clean
clean: ## Remove build and coverage output
	rm -f $(BIN) coverage.out
