.PHONY: run test race vet build fmt

run:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

test:
	go test -count=1 ./...

race:
	go test -race -count=1 ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

