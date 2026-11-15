APP     := urban-crisis-evacuation
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: run test vet build docker up down logs tidy

run: ## run locally with in-memory store
	go run ./cmd/server

test:
	go test ./... -race -count=1

vet:
	go vet ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/server ./cmd/server

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(APP):latest -t $(APP):$(VERSION) .

up: ## app + postgres
	VERSION=$(VERSION) docker compose up --build -d

down:
	docker compose down

logs:
	docker compose logs -f app

tidy:
	go mod tidy
