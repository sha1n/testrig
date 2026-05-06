PROJECTNAME := "testrig"

# Go related variables.
GOFILES := $(shell find . -type f -name '*.go' -not -path './vendor/*' -not -path './.git/*')

MODFLAGS=-mod=readonly

# Make is verbose in Linux. Make it silent.
MAKEFLAGS += --silent

.PHONY: default
default: check

## check: Runs format, lint and tests
.PHONY: check
check: format lint test

## install: Checks for missing dependencies and installs them
.PHONY: install
install: go-get

## format: Formats Go source files
.PHONY: format
format: go-format

## lint: Runs all linters including go vet and golangci-lint
.PHONY: lint
lint: go-lint golangci-lint

## test: Runs all Go tests
.PHONY: test
test: go-test

## clean: Removes build and test artifacts
.PHONY: clean
clean:
	@echo "  >  Cleaning build cache"
	go clean $(MODFLAGS) ./...
	rm -rf bin

.PHONY: go-lint
go-lint:
	@echo "  >  Linting source files..."
	@PKGS=`go list $(MODFLAGS) ./... 2>/dev/null`; if [ -n "$$PKGS" ]; then go vet $(MODFLAGS) $$PKGS; fi

## golangci-lint: Runs golangci-lint
.PHONY: golangci-lint
golangci-lint:
	@echo "  >  Running golangci-lint..."
	golangci-lint run 2> /dev/null || echo "  !  golangci-lint not found, skipping..."

.PHONY: go-format
go-format:
	@echo "  >  Formating source files..."
	@if [ -n "$(GOFILES)" ]; then gofmt -s -w $(GOFILES); fi

## coverage: Runs tests and displays coverage (all packages, including examples)
.PHONY: coverage
coverage:
	@echo "  >  Running tests with coverage..."
	@PKGS=`go list $(MODFLAGS) ./... 2>/dev/null`; \
	if [ -z "$$PKGS" ]; then echo "  !  No packages found, skipping coverage."; exit 0; fi; \
	go test $(MODFLAGS) -coverprofile=coverage.out -covermode=count -coverpkg=./... ./...; \
	go tool cover -func=coverage.out

.PHONY: go-test
go-test:
	@echo "  >  Running Go tests..."
	@PKGS=`go list $(MODFLAGS) ./... 2>/dev/null`; if [ -n "$$PKGS" ]; then go test $(MODFLAGS) -v -covermode=count $$PKGS; fi

.PHONY: go-get
go-get:
	@echo "  >  Checking if there is any missing dependencies..."
	go mod tidy

## build-examples: Builds all example binaries into bin/
.PHONY: build-examples
build-examples:
	@echo "  >  Building example binaries..."
	@mkdir -p bin
	@for dir in $(shell find examples -name 'main.go' -exec dirname {} \;); do \
		name=$$(basename $$dir); \
		echo "  >  Building $$name..."; \
		go build $(MODFLAGS) -o bin/$$name ./$$dir; \
	done

.PHONY: all
all: help

.PHONY: help
help: Makefile
	@echo
	@echo " Choose a command run in "$(PROJECTNAME)":"
	@echo
	@sed -n 's/^##//p' $< | column -t -s ':' |  sed -e 's/^/ /'
	@echo
