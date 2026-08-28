.PHONY: build build-adapter build-all package test fmt vet clean

build:
	go build -o bin/codeflow ./cmd/codeflow

build-adapter:
	mkdir -p bin
	dart compile exe adapters/dart/bin/codeflow_dart_adapter.dart -o bin/dart-adapter

build-all: build build-adapter

package: build-all
	mkdir -p dist
	tar -czvf dist/codeflow-local.tar.gz -C . bin/codeflow bin/dart-adapter skills/codeflow

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

clean:
	rm -rf bin dist
