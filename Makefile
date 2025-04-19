default: build

.PHONY: help
help: ## Show this help message
	@grep -E '(^[0-9a-zA-Z_-]+:.*?##.*$$)|(^##)' $(MAKEFILE_LIST) | \
	awk 'BEGIN {FS = ":.*?## "}; {printf "\033[32m%-25s\033[0m %s\n", $$1, $$2}' | \
	sed -e 's/\[32m##/[33m/'

GO ?= go
GOLANGCI_LINT_VERSION ?= v1.55.2

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
build: ## Build the binary
	$(GO) test ./...
	$(GO) build ./...

.PHONY: install
install: ## Install the binary
	$(GO) install ./...

.PHONY: clean
clean: ## Remove build artifacts
	$(GO) clean

.PHONY: git-bump-tag
git-bump-tag: ## Bump minor version and push git tag
	@CURR_TAG=$$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0"); \
	MAJOR=$$(echo $$CURR_TAG | cut -d. -f1); \
	MINOR=$$(echo $$CURR_TAG | cut -d. -f2); \
	PATCH=$$(echo $$CURR_TAG | cut -d. -f3); \
	NEW_PATCH=$$((PATCH + 1)); \
	NEW_TAG="$$MAJOR.$$MINOR.$$NEW_PATCH"; \
	git tag $$NEW_TAG && git push origin $$NEW_TAG
