.PHONY: build test fmt vet clean

build:
	go build -o bin/codeflow ./cmd/codeflow

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

clean:
	rm -rf bin
