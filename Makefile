.PHONY: all build test test-race cover vet fmt lint tidy tidy-check ci example clean

all: build test

## build: compile all packages (binaries go to /dev/null — no artifacts left behind)
build:
	go build .
	go build -o /dev/null ./examples/api/stdlibmux
	go build -o /dev/null ./examples/api/gin
	go build -o /dev/null ./examples/api/authed-gin
	go build -o /dev/null ./examples/api/api-auth-gin
	go build -o /dev/null ./examples/requests

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

## cover-ci: coverage run matching CI (race detector + atomic mode)
cover-ci:
	go test -race -count=1 -timeout 120s -coverprofile=coverage.out -covermode=atomic ./...
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

## tidy-check: verify go.mod/go.sum match `go mod tidy` (matches CI gate)
tidy-check:
	go mod tidy
	@git diff --exit-code go.mod go.sum || (echo "go.mod/go.sum not tidy — run 'make tidy' and commit" && exit 1)

## ci: run the full CI pipeline locally (build → vet → tidy-check → race tests)
ci: build vet tidy-check test-race

## example: build all example servers
example:
	go build -o bin/stdlibmux ./examples/api/stdlibmux
	go build -o bin/gin ./examples/api/gin
	go build -o bin/authed-gin ./examples/api/authed-gin
	go build -o bin/api-auth-gin ./examples/api/api-auth-gin

## run-stdlib: build and run the stdlib mux example
run-stdlib: example
	./bin/stdlibmux

## run-gin: build and run the gin example
run-gin: example
	./bin/gin

## run-authed-gin: build and run the basic-auth gin example
run-authed-gin: example
	./bin/authed-gin

## run-api-auth-gin: build and run the api-token gin example
run-api-auth-gin: example
	./bin/api-auth-gin

## clean: remove build artifacts
clean:
	rm -rf bin coverage.out *.db stdlibmux gin
