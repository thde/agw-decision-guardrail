APP_NAME ?= agw-decision-guardrail

# try getconf (linux / macos), getconf (BSD), nproc, then fallback to 1
NPROCS := $(shell getconf _NPROCESSORS_ONLN 2>/dev/null || getconf NPROCESSORS_ONLN 2>/dev/null || nproc 2>/dev/null || echo 1)
MAKEFLAGS += --jobs=$(NPROCS)

.PHONY: all build generate test lint lint-fix mod-tidy vet golangci-lint staticcheck modernize govulncheck gohawk typos sembr update clean help

all: build

build:
	goreleaser build --clean --snapshot --single-target

generate:
	go generate ./...

test:
	go test -race ./...

lint: mod-tidy vet staticcheck golangci-lint modernize govulncheck gohawk typos sembr

lint-fix:
	go mod tidy
	golangci-lint run --fix
	go fix ./...
	typos -w
	$(MAKE) lint

mod-tidy:
	go mod tidy -diff

vet:
	go vet ./...

golangci-lint:
	golangci-lint config verify
	golangci-lint run

staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@latest ./...

modernize:
	go fix -diff ./...

govulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

gohawk:
	go run github.com/kojah/gohawk@latest ./...

typos:
	typos

sembr:
	go run thde.io/sembr/cmd/sembr@latest ./...

update:
	go get -v -u ./...
	go mod tidy
	$(MAKE) test

clean:
	rm -rf dist/

help:
	@echo "make           # Build $(APP_NAME)"
	@echo "make test      # Run tests"
	@echo "make generate  # Generate code"
	@echo "make lint-fix  # Run linters and try fix issues"
	@echo "make lint      # Run linters"
	@echo "make update    # Update dependencies"
	@echo "make clean     # Remove built app"
