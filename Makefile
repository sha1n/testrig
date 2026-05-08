PROJECTNAME := "testrig"

# Modules in this multi-module workspace. Order matters for tidy
# (root before consumers).
MODULES := . services/oidc services/postgres services/wiremock examples

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
	@for mod in $(MODULES); do \
		(cd $$mod && go clean $(MODFLAGS) ./...); \
	done
	rm -rf bin coverage.out

.PHONY: go-lint
go-lint:
	@echo "  >  Linting source files..."
	@for mod in $(MODULES); do \
		echo "  >  vet $$mod"; \
		(cd $$mod && PKGS=`go list $(MODFLAGS) ./... 2>/dev/null`; \
		 if [ -n "$$PKGS" ]; then go vet $(MODFLAGS) $$PKGS; fi); \
	done

## golangci-lint: Runs golangci-lint via the version pinned in tools/go.mod (fails the target when issues are reported)
.PHONY: golangci-lint
golangci-lint:
	@echo "  >  Running golangci-lint (pinned via tools/)..."
	@for mod in $(MODULES); do \
		echo "  >  golangci-lint $$mod"; \
		(cd $$mod && go tool golangci-lint run ./...) || exit $$?; \
	done

.PHONY: go-format
go-format:
	@echo "  >  Formating source files..."
	@if [ -n "$(GOFILES)" ]; then gofmt -s -w $(GOFILES); fi

## coverage: Runs tests with coverage in every module (writes <module>/coverage.out)
.PHONY: coverage
coverage:
	@echo "  >  Running tests with coverage..."
	@for mod in $(MODULES); do \
		echo "  >  coverage $$mod"; \
		(cd $$mod && PKGS=`go list $(MODFLAGS) ./... 2>/dev/null`; \
		 if [ -z "$$PKGS" ]; then exit 0; fi; \
		 go test $(MODFLAGS) -coverprofile=coverage.out -covermode=count -coverpkg=./... ./... && \
		 go tool cover -func=coverage.out | tail -n 1); \
	done

.PHONY: go-test
go-test:
	@echo "  >  Running Go tests..."
	@for mod in $(MODULES); do \
		echo "  >  test $$mod"; \
		(cd $$mod && PKGS=`go list $(MODFLAGS) ./... 2>/dev/null`; \
		 if [ -n "$$PKGS" ]; then go test $(MODFLAGS) -v -covermode=count $$PKGS; fi); \
	done

.PHONY: go-get
go-get:
	@echo "  >  Syncing workspace (go work sync)..."
	@go work sync
	@echo "  >  Note: per-module 'go mod tidy' is intentionally not used —"
	@echo "          'github.com/sha1n/testrig' has no published version yet,"
	@echo "          so tidy outside the workspace fails to resolve it."

## build-examples: Builds all example binaries into bin/
.PHONY: build-examples
build-examples:
	@echo "  >  Building example binaries..."
	@mkdir -p bin
	@for dir in $(shell find examples -name 'main.go' -exec dirname {} \;); do \
		name=$$(basename $$dir); \
		rel=$${dir#examples/}; \
		echo "  >  Building $$name..."; \
		(cd examples && go build $(MODFLAGS) -o ../bin/$$name ./$$rel); \
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
