.PHONY: build build-adapter build-ui test-ui test-flowview build-all package test test-all fmt vet check-naming clean

build: build-ui
	mkdir -p bin
	go build -o bin/codeflow ./cmd/codeflow

build-adapter:
	mkdir -p bin
	dart compile exe adapters/dart/bin/codeflow_dart_adapter.dart -o bin/dart-adapter

build-ui:
	cd web/flowview && npm run check && npm run build
	perl -pi -e 's/[ \t]+$$//' web/flowview/dist/index.html
	cp web/flowview/dist/index.html internal/presenter/flowview/svelte_flow_view.html
	mkdir -p docs/samples
	cp web/flowview/dist/index.html docs/samples/live-semantic-map-prototype.html

test-ui:
	cd web/flowview && npm test

test-flowview:
	cd web/live-comprehension-workspace && npm run test:flowview

build-all: build build-adapter

package: build-all
	mkdir -p dist
	tar -czvf dist/codeflow-local.tar.gz -C . bin/codeflow bin/dart-adapter skills/codeflow adapters/typescript

test:
	CGO_ENABLED=0 go test ./...

test-all:
	CGO_ENABLED=0 go test -count=1 ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

check-naming:
	bash scripts/check-naming-conventions.sh

clean:
	rm -rf bin dist
