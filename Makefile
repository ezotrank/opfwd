default: build

.PHONY: help
help: ## Show this help message
	@grep -E '(^[0-9a-zA-Z_-]+:.*?##.*$$)|(^##)' $(MAKEFILE_LIST) | \
	awk 'BEGIN {FS = ":.*?## "}; {printf "\033[32m%-25s\033[0m %s\n", $$1, $$2}' | \
	sed -e 's/\[32m##/[33m/'

GO ?= go
GOLANGCI_LINT_VERSION ?= v1.55.2

# Project metadata
PROJECT_NAME ?= opfwd
BINARY_LINUX ?= $(PROJECT_NAME).linux
BINARY_DARWIN ?= $(PROJECT_NAME).darwin
INSTALL_DIR ?= $(HOME)/.local/bin

.PHONY: dev-deps-install
dev-deps-install: ## Install development dependencies
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: lint
lint: ## Lint source code
	golangci-lint run
	$(GO) vet ./...

.PHONY: fmt
fmt: ## Format code
	go fmt ./...

.PHONY: build
build: ## Build the binary for Linux and Darwin (arm64)
	$(GO) test ./...
	GOOS=linux GOARCH=arm64 $(GO) build -o $(BINARY_LINUX)
	GOOS=darwin GOARCH=arm64 $(GO) build -o $(BINARY_DARWIN)

.PHONY: install
install: build ## Install the binaries to ~/.local/bin (overwrites existing files)
	mkdir -p $(INSTALL_DIR)
	cp -f $(BINARY_LINUX) $(BINARY_DARWIN) $(INSTALL_DIR)/

.PHONY: clean
clean: ## Remove build artifacts
	rm -f $(BINARY_LINUX) $(BINARY_DARWIN)
