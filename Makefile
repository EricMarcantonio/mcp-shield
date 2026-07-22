.PHONY: build run test test-unit test-integration lint docker-build docker-up docker-down clean

build:
	mkdir -p bin
	go build -o bin/mcp-shield ./cmd/gateway
	go build -o bin/mcp-shield-testserver ./cmd/server

run: build
	./bin/mcp-shield

test: test-unit test-integration

test-unit:
	go test ./...

test-integration: build
	go test -tags=integration ./test/...

lint:
	@fmt_out=$$(gofmt -l .); \
	if [ -n "$$fmt_out" ]; then \
		echo "gofmt needs to be run on:"; echo "$$fmt_out"; exit 1; \
	fi
	go vet ./...

docker-build:
	docker compose build

docker-up:
	docker compose up -d

docker-down:
	docker compose down

clean:
	rm -rf bin
	rm -f data/*.db data/*.db-wal data/*.db-shm
