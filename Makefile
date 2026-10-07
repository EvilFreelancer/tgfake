.PHONY: build run test test-race cover lint check snapshot release-check install-test clean

# The version a local build reports: the nearest tag, or the commit.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GORELEASER ?= goreleaser

# build/tgfake for the host, the way a release links it.
build:
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o build/tgfake ./cmd/tgfake

# The stand with its scripted model on 127.0.0.1:18790.
run:
	go run ./cmd/tgfake --llm

test:
	go test ./...

test-race:
	go test -race -count=1 ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1

lint:
	golangci-lint run ./...

# What CI's test and lint jobs run, in one go.
check: lint test-race

# Every release archive and checksums.txt into dist/, without publishing.
snapshot:
	$(GORELEASER) release --snapshot --clean

release-check:
	$(GORELEASER) check

# scripts/install.sh against the archives of `make snapshot`.
install-test:
	sh scripts/test-install.sh dist

clean:
	rm -rf build dist coverage.out
