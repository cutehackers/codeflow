.PHONY: build build-flowmeter build-adapter build-ui test-ui build-all package test fmt vet check-naming clean

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

build-ui:
	cd web/flowview && npm run check && npm run build
	cp web/flowview/dist/index.html internal/flowview/svelte_flow_view.html

test-ui:
	cd web/flowview && npm test

build-all: build build-adapter

package: build-all
	mkdir -p dist
	tar -czvf dist/codeflow-local.tar.gz -C . bin/codeflow bin/flowmeter bin/dart-adapter skills/codeflow adapters/typescript

test:
	go test -p 1 -count=1 ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

check-naming:
	bash scripts/check-naming-conventions.sh

clean:
	rm -rf bin dist
