.PHONY: up down logs build test test-integration lint fmt check-comments tidy

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f gateway

build:
	go build -trimpath -o bin/ ./cmd/...

test:
	go test -race ./...

test-integration:
	go test -race -tags integration ./...

lint: check-comments
	golangci-lint run

fmt:
	golangci-lint fmt

check-comments:
	./scripts/check-no-comments.sh

tidy:
	go mod tidy
