.PHONY: help build run test test-race lint fmt docker-build docker-up docker-down docker-logs observability-up observability-down simulate clean check ci version release-check git-hooks
help:
	@echo "build run test test-race lint fmt docker-build docker-up docker-down docker-logs observability-up observability-down simulate clean check ci version release-check git-hooks"
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
check:
	go run ./cmd/dev-check branch
	go run ./cmd/dev-check release-config
	go run ./cmd/dev-check pr-commits
ci:
	go mod verify
	go vet ./...
	$(MAKE) lint
	$(MAKE) test-race
	$(MAKE) simulate
	CGO_ENABLED=0 go build -trimpath -o /dev/null ./cmd/election-bot
version:
	go run ./cmd/election-bot version
release-check:
	go run ./cmd/dev-check release-config
git-hooks:
	@mkdir -p .git/hooks
	@printf '#!/bin/sh\nexec go run ./cmd/dev-check commit "$$1"\n' > .git/hooks/commit-msg
	@chmod +x .git/hooks/commit-msg
	@echo "Installed .git/hooks/commit-msg hook"
