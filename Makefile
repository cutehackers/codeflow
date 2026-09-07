.PHONY: build build-flowmeter build-adapter build-all package test fmt vet clean

build:
	mkdir -p bin
	go build -o bin/codeflow ./cmd/codeflow
	go build -o bin/flowmeter ./cmd/flowmeter

build-flowmeter:
	mkdir -p bin
	go build -o bin/flowmeter ./cmd/flowmeter

build-adapter:
	mkdir -p bin
	dart compile exe adapters/dart/bin/codeflow_dart_adapter.dart -o bin/dart-adapter

build-all: build build-adapter

package: build-all
	mkdir -p dist
	tar -czvf dist/codeflow-local.tar.gz -C . bin/codeflow bin/flowmeter bin/dart-adapter skills/codeflow adapters/typescript

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

clean:
	rm -rf bin dist
