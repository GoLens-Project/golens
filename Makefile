.PHONY: all build test test-race cover vet fmt lint tidy example clean

all: build test

## build: compile all packages (binaries go to /dev/null — no artifacts left behind)
build:
	go build .
	go build -o /dev/null ./examples/stdlibmux
	go build -o /dev/null ./examples/gin
	go build -o /dev/null ./examples/authed-gin
	go build -o /dev/null ./examples/api-auth-gin

## test: run the unit tests
test:
	go test -count=1 -timeout 120s ./...

## test-race: run tests with the race detector
test-race:
	go test -race -count=1 -timeout 180s ./...

## cover: run tests and print a coverage summary
cover:
	go test -count=1 -timeout 120s -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

## vet: run go vet
vet:
	go vet ./...

## fmt: format all Go sources
fmt:
	gofmt -w .
	goimports -w -local golens . 2>/dev/null || true

## lint: run golangci-lint (if installed)
lint:
	golangci-lint run ./...

## tidy: ensure go.mod/go.sum are clean
tidy:
	go mod tidy

## example: build all example servers
example:
	go build -o bin/stdlibmux ./examples/stdlibmux
	go build -o bin/gin ./examples/gin

## run-stdlib: build and run the stdlib mux example
run-stdlib: example
	./bin/stdlibmux

## run-gin: build and run the gin example
run-gin: example
	./bin/gin

## clean: remove build artifacts
clean:
	rm -rf bin coverage.out *.db stdlibmux gin
