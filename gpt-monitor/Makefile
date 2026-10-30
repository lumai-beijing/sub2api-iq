.PHONY: dev test build docker

dev:
	cd frontend && npm run dev

test:
	go test ./...
	cd frontend && npm test

build:
	cd frontend && npm run build
	go build ./cmd/server

docker:
	docker compose build
