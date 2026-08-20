SHELL := /bin/sh

.PHONY: doctor-dev test build local package package-macos

doctor-dev:
	@set -eu; \
	for tool in git go dart flutter node npm sqlite3 uv jq curl; do \
		command -v "$$tool" >/dev/null 2>&1 || { echo "missing required tool: $$tool"; exit 1; }; \
	done; \
	echo "git: $$(git --version)"; \
	echo "go: $$(go version)"; \
	echo "dart: $$(dart --version 2>&1)"; \
		printf "flutter: "; \
		flutter --version --machine | jq -r '"\(.flutterVersion) (Dart \(.dartSdkVersion))"'; \
	echo "node: $$(node --version)"; \
	echo "npm: $$(npm --version)"; \
	echo "sqlite: $$(sqlite3 --version | awk '{print $$1}')"; \
	if command -v cgc >/dev/null 2>&1; then \
		echo "CodeGraphContext: $$(cgc version 2>&1 | head -1)"; \
	else \
		echo "CodeGraphContext: on-demand via uvx (not globally installed)"; \
	fi

test:
	go test ./...

build:
	go build -o bin/codeflow ./core/cmd/codeflow

# Fast local layout. The AOT adapter avoids paying Dart JIT startup on every
# FlowView refresh; both output directories are ignored by Git.
local:
	@set -eu; mkdir -p bin libexec; \
	go build -o bin/codeflow ./core/cmd/codeflow; \
	(cd adapters/dart && dart pub get >/dev/null && dart compile exe bin/codeflow-dart-adapter.dart -o ../../libexec/codeflow-dart-adapter); \
	cp assets/compatibility.json libexec/compatibility.json; \
	chmod 755 bin/codeflow libexec/codeflow-dart-adapter; \
	echo "local CodeFlow: bin/codeflow"

# Produces a relocatable unsigned macOS layout. Signing/notarization remains a
# release concern; the executable resolves its adjacent libexec adapter.
package-macos:
	@set -eu; rm -rf dist/codeflow-macos; mkdir -p dist/codeflow-macos/bin dist/codeflow-macos/libexec; \
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -o dist/codeflow-macos/bin/codeflow ./core/cmd/codeflow; \
	(cd adapters/dart && dart pub get && dart compile exe bin/codeflow-dart-adapter.dart -o ../../dist/codeflow-macos/libexec/codeflow-dart-adapter); \
	cp assets/compatibility.json dist/codeflow-macos/libexec/compatibility.json; \
	cp -R plugins dist/codeflow-macos/plugins; cp -R .agents dist/codeflow-macos/.agents; \
	chmod 755 dist/codeflow-macos/bin/codeflow dist/codeflow-macos/libexec/codeflow-dart-adapter; \
	echo "codeflow macOS package: dist/codeflow-macos"

# Native layout used by the isolated installation contract test.
package:
	@set -eu; rm -rf dist/codeflow; mkdir -p dist/codeflow/bin dist/codeflow/libexec; \
	go build -o dist/codeflow/bin/codeflow ./core/cmd/codeflow; \
	(cd adapters/dart && dart pub get && dart compile exe bin/codeflow-dart-adapter.dart -o ../../dist/codeflow/libexec/codeflow-dart-adapter); \
	cp assets/compatibility.json dist/codeflow/libexec/; \
	cp -R plugins dist/codeflow/plugins; cp -R .agents dist/codeflow/.agents; \
	chmod 755 dist/codeflow/bin/codeflow dist/codeflow/libexec/codeflow-dart-adapter
