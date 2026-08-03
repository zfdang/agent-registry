.PHONY: build run test vet fmt clean

BINARY := bin/registry-api

build:
	CGO_ENABLED=0 go build -o $(BINARY) .

run:
	go run .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

clean:
	rm -rf bin
