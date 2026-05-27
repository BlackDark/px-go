APP := px

.PHONY: tidy fmt test build lint docker-build

tidy:
	go mod tidy

fmt:
	gofmt -w ./cmd ./internal

test:
	go test ./...

build:
	go build ./...

lint:
	golangci-lint run ./...

docker-build:
	docker build -f docker/Dockerfile -t $(APP):local .
