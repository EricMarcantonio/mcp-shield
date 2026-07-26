COVER_THRESHOLD := 70
FUZZTIME        := 15s

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build gateway and test server into bin/
	mkdir -p bin
	go build -o bin/mcp-shield ./cmd/gateway
	go build -o bin/mcp-shield-testserver ./cmd/server

.PHONY: run
run: build ## Build and run the gateway locally
	./bin/mcp-shield

.PHONY: fmt
fmt: ## Format code (gofumpt + goimports via golangci-lint)
	golangci-lint fmt ./...

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run ./...

.PHONY: lint-fix
lint-fix: ## Run golangci-lint with autofix
	golangci-lint run --fix ./...

.PHONY: vuln
vuln: ## Scan for known vulnerabilities (govulncheck)
	govulncheck ./...

.PHONY: test
test: test-unit test-integration ## Unit + integration tests

.PHONY: test-unit
test-unit: ## Unit tests
	go test ./...

.PHONY: test-race
test-race: ## Unit tests with the race detector
	go test -race ./...

.PHONY: test-integration
test-integration: build ## Integration tests (needs built binaries)
	go test -tags=integration ./test/...

.PHONY: fuzz
fuzz: ## Run each fuzz target for $(FUZZTIME)
	go test -run=^$$ -fuzz=FuzzCanonicalizeValueStable -fuzztime=$(FUZZTIME) ./internal/manifest
	go test -run=^$$ -fuzz=FuzzManifestHashOrderInvariance -fuzztime=$(FUZZTIME) ./internal/manifest
	go test -run=^$$ -fuzz=FuzzFromCanonicalJSONRoundTrip -fuzztime=$(FUZZTIME) ./internal/manifest

.PHONY: cover
cover: ## Unit coverage on internal/, enforced >= $(COVER_THRESHOLD)%
	go test -coverprofile=coverage.out -covermode=atomic ./internal/...
	@total=$$(go tool cover -func=coverage.out | awk '/^total:/ {sub("%","",$$3); print $$3}'); \
	echo "total coverage: $$total% (floor: $(COVER_THRESHOLD)%)"; \
	awk -v t=$$total -v min=$(COVER_THRESHOLD) 'BEGIN { exit (t+0 < min) ? 1 : 0 }'

.PHONY: cover-html
cover-html: cover ## Open HTML coverage report
	go tool cover -html=coverage.out

.PHONY: docker-build
docker-build: ## Build docker image via compose
	docker compose build

.PHONY: docker-up
docker-up: ## Start via docker compose
	docker compose up -d

.PHONY: docker-down
docker-down: ## Stop docker compose
	docker compose down

.PHONY: release-snapshot
release-snapshot: ## Local GoReleaser dry run (no publish)
	goreleaser release --snapshot --clean

.PHONY: clean
clean: ## Remove build artifacts and local databases
	rm -rf bin dist coverage.out
	rm -f data/*.db data/*.db-wal data/*.db-shm
