.PHONY: help build run test test-race lint fmt docker-build docker-up docker-down docker-logs observability-up observability-down simulate clean
help:
	@echo "build run test test-race lint fmt docker-build docker-up docker-down docker-logs observability-up observability-down simulate clean"
build:
	go build -trimpath -o bin/election-bot ./cmd/election-bot
run:
	go run ./cmd/election-bot
test:
	go test ./...
test-race:
	go test -race ./...
lint:
	go vet ./...
	@golangci-lint version | rg -q "version 2.13.2 "
	golangci-lint run
fmt:
	gofmt -w cmd internal migrations test
docker-build:
	docker build -t election-bot .
docker-up:
	docker compose up -d --build
docker-down:
	docker compose down
docker-logs:
	docker compose logs -f election-bot
observability-up:
	docker compose -f compose.yaml -f compose.observability.yaml up -d --build
observability-down:
	docker compose -f compose.yaml -f compose.observability.yaml down
simulate:
	go run ./cmd/election-bot --simulate
clean:
	rm -rf bin coverage.out
