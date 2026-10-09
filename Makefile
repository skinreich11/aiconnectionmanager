SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

GO ?= go
GOFILES := $(shell find . -type f -name '*.go' -not -path './.git/*' -not -path './.tools/*')

TOOLS_DIR ?= $(CURDIR)/.tools
TOOLS_BIN ?= $(TOOLS_DIR)/bin
PATH := $(TOOLS_BIN):$(PATH)

GOLANGCI_LINT_VERSION ?= v2.14.0
GOLANGCI_LINT_CACHE ?= $(TOOLS_DIR)/golangci-cache
GOVULNCHECK_VERSION ?= v1.8.0
ACTIONLINT_VERSION ?= v1.7.12
PMD_VERSION ?= 7.27.0
PMD_MINIMUM_TOKENS ?= 100
HADOLINT_IMAGE ?= hadolint/hadolint:2.15.1

GOLANGCI_LINT := $(TOOLS_BIN)/golangci-lint
GOVULNCHECK := $(TOOLS_BIN)/govulncheck
ACTIONLINT := $(TOOLS_BIN)/actionlint
PMD_DIR := $(TOOLS_DIR)/pmd-$(PMD_VERSION)
PMD := $(PMD_DIR)/bin/pmd
PMD_ARCHIVE := $(TOOLS_DIR)/pmd-dist-$(PMD_VERSION)-bin.zip
PMD_URL := https://github.com/pmd/pmd/releases/download/pmd_releases%2F$(PMD_VERSION)/pmd-dist-$(PMD_VERSION)-bin.zip

COVERAGE_FILE ?= coverage.out
IMAGE ?= aiconnectionmanager:local
CONTAINER ?= aiconnectionmanager
HOST_PORT ?= 8443
SONAR_SCANNER ?= sonar-scanner
SONAR_PROJECT_KEY ?= aiconnectionmanager
SONAR_ORGANIZATION ?=

.PHONY: help tools format-check vet test test-race coverage build \
	staticcheck golangci-lint gosec govulncheck workflow-lint pmd-cpd \
	container-lint lint quality ci docker-check docker-build docker-run \
	docker-smoke sonar

help: ## Show available commands
	grep -E '^[a-zA-Z0-9_.-]+:.*## ' $(MAKEFILE_LIST) | \
		sed 's/:.*## /\t/'

$(TOOLS_BIN):
	mkdir -p "$@"

$(GOLANGCI_LINT): | $(TOOLS_BIN)
	GOBIN="$(TOOLS_BIN)" $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(GOVULNCHECK): | $(TOOLS_BIN)
	GOBIN="$(TOOLS_BIN)" $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

$(ACTIONLINT): | $(TOOLS_BIN)
	GOBIN="$(TOOLS_BIN)" $(GO) install github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

$(PMD):
	command -v java >/dev/null 2>&1 || { echo 'Java is required for PMD/CPD.' >&2; exit 1; }; \
	command -v curl >/dev/null 2>&1 || { echo 'curl is required to download PMD.' >&2; exit 1; }; \
	command -v unzip >/dev/null 2>&1 || { echo 'unzip is required to install PMD.' >&2; exit 1; }; \
	mkdir -p "$(TOOLS_DIR)"; \
	temporary_directory="$$(mktemp -d)"; \
	trap 'rm -rf "$${temporary_directory}"' EXIT; \
	curl --fail --location --silent --show-error "$(PMD_URL)" --output "$(PMD_ARCHIVE)"; \
	unzip -q "$(PMD_ARCHIVE)" -d "$${temporary_directory}"; \
	mv "$${temporary_directory}/pmd-bin-$(PMD_VERSION)" "$(PMD_DIR)"; \
	rm -f "$(PMD_ARCHIVE)"

tools: $(GOLANGCI_LINT) $(GOVULNCHECK) $(ACTIONLINT) $(PMD) ## Install pinned quality tools

format-check: ## Verify Go source formatting
	unformatted_files="$$(gofmt -l $(GOFILES))"; \
	if [[ -n "$${unformatted_files}" ]]; then \
		echo 'Go files are not formatted:' >&2; \
		echo "$${unformatted_files}" >&2; \
		exit 1; \
	fi

vet: ## Run the standard Go vet analyzers
	$(GO) vet ./...

test: ## Run unit and integration tests
	$(GO) test ./...

test-race: ## Run tests with the Go race detector
	$(GO) test -race ./...

coverage: ## Generate the coverage report consumed by SonarQube
	$(GO) test -coverpkg=./... -covermode=atomic -coverprofile="$(COVERAGE_FILE)" ./...

build: ## Compile every Go package
	$(GO) build ./...

staticcheck: $(GOLANGCI_LINT) ## Run Staticcheck through golangci-lint
	GOLANGCI_LINT_CACHE="$(GOLANGCI_LINT_CACHE)" $(GOLANGCI_LINT) run ./... --enable-only=staticcheck --timeout=5m

golangci-lint: $(GOLANGCI_LINT) ## Run the configured multi-linter suite
	GOLANGCI_LINT_CACHE="$(GOLANGCI_LINT_CACHE)" $(GOLANGCI_LINT) run ./... --timeout=5m

gosec: $(GOLANGCI_LINT) ## Run gosec through golangci-lint
	GOLANGCI_LINT_CACHE="$(GOLANGCI_LINT_CACHE)" $(GOLANGCI_LINT) run ./... --enable-only=gosec --timeout=5m

govulncheck: $(GOVULNCHECK) ## Scan reachable dependencies for known vulnerabilities
	$(GOVULNCHECK) ./...

workflow-lint: $(ACTIONLINT) ## Lint GitHub Actions workflow files
	$(ACTIONLINT) .github/workflows/*.yml

pmd-cpd: $(PMD) ## Run PMD's Go-capable copy/paste detector
	# PMD has no Go rules; CPD is its supported Go analysis component.
	$(PMD) cpd --language go --minimum-tokens "$(PMD_MINIMUM_TOKENS)" \
		--dir internal --dir cmd --dir main.go --dir hello/hello.go

container-lint: ## Lint the Dockerfile with Hadolint
	if command -v hadolint >/dev/null 2>&1; then \
		hadolint --failure-threshold error Dockerfile; \
	else \
		command -v docker >/dev/null 2>&1 || { echo 'Hadolint or Docker is required for container linting.' >&2; exit 1; }; \
		docker info >/dev/null 2>&1 || { echo 'Docker is not running.' >&2; exit 1; }; \
		docker run --rm --interactive "$(HADOLINT_IMAGE)" --failure-threshold error - < Dockerfile; \
	fi

lint: format-check vet staticcheck golangci-lint gosec govulncheck workflow-lint pmd-cpd ## Run code-quality and security linters

quality: lint test-race coverage build ## Run all local quality gates

ci: quality container-lint docker-build docker-smoke ## Run quality gates and validate the container

docker-check: ## Verify Docker is available
	command -v docker >/dev/null 2>&1 || { echo 'Docker CLI was not found.' >&2; exit 1; }
	docker info >/dev/null 2>&1 || { echo 'Docker is not running.' >&2; exit 1; }

docker-build: docker-check ## Build the application image
	docker build --tag "$(IMAGE)" .

docker-run: docker-build ## Build and run the application image
	docker rm --force "$(CONTAINER)" >/dev/null 2>&1 || true; \
	exec docker run --init --rm --name "$(CONTAINER)" --publish "$(HOST_PORT):8443" "$(IMAGE)"

docker-smoke: docker-build ## Build, start an image, and verify its HTTPS endpoint responds
	command -v curl >/dev/null 2>&1 || { echo 'curl is required for the Docker smoke test.' >&2; exit 1; }; \
	trap 'docker logs "$(CONTAINER)" || true; docker rm --force "$(CONTAINER)" >/dev/null 2>&1 || true' EXIT; \
	docker run --detach --name "$(CONTAINER)" --publish "$(HOST_PORT):8443" "$(IMAGE)"; \
	for attempt in {1..30}; do \
		status="$$(curl --silent --show-error --insecure --output /dev/null --write-out '%{http_code}' "https://127.0.0.1:$(HOST_PORT)/" || true)"; \
		if [[ "$${status}" == '404' ]]; then exit 0; fi; \
		sleep 1; \
	done; \
	echo 'Container did not become ready within 30 seconds.' >&2; \
	exit 1

sonar: coverage ## Run a local SonarQube scan and wait for its quality gate
	command -v "$(SONAR_SCANNER)" >/dev/null 2>&1 || { echo 'sonar-scanner is required for local SonarQube scans.' >&2; exit 1; }; \
	: "$${SONAR_TOKEN:?SONAR_TOKEN must be set for a SonarQube scan}"; \
	scanner_arguments=("-Dsonar.projectKey=$(SONAR_PROJECT_KEY)" "-Dsonar.go.coverage.reportPaths=$(COVERAGE_FILE)" '-Dsonar.qualitygate.wait=true'); \
	if [[ -n "$(SONAR_ORGANIZATION)" ]]; then scanner_arguments+=("-Dsonar.organization=$(SONAR_ORGANIZATION)"); fi; \
	"$(SONAR_SCANNER)" "$${scanner_arguments[@]}"
