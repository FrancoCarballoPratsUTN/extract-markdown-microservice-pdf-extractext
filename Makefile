.PHONY: dev-up dev-down install test test-unit test-integration build run

install:
	go run github.com/yfedoseev/pdf_oxide/go/cmd/install@v0.3.78 -shared

dev-up:
	docker compose up -d --build

dev-down:
	docker compose down

test:
	CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...

test-unit:
	CGO_ENABLED=0 go test -tags unit ./...

test-integration:
	CGO_ENABLED=0 go test -tags integration -v -timeout 5m ./internal/infrastructure/pdf/...

build:
	CGO_ENABLED=0 go build -o bin/server ./cmd/server

run: build
	./bin/server
