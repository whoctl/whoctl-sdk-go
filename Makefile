# whoctl-sdk-go
#
# Nothing here touches a machine. These tests are the only ones in the workspace
# that need no fixture tree at all, which is why this is the one module with no
# container and no sandbox: there is nothing to run them against.
#
# It has a Makefile anyway, because `make test` working in every repository is
# the rule, and a module where it does not is the one somebody forgets to run.

.DEFAULT_GOAL := help

## test: the whole suite. Safe anywhere: nothing here reads or writes a system.
.PHONY: test
test:
	@go test ./...

## standalone: build and test with the workspace off, the way a consumer sees it
#
# The SDK depends on nothing in this checkout, so this is cheaper than the
# script the other modules borrow from whoctl — but it answers the same
# question, and answering it here means a broken SDK is caught before the four
# modules that build against it are.
.PHONY: standalone
standalone:
	@GOWORK=off go build ./...
	@GOWORK=off go test ./...
	@echo "standalone: github.com/whoctl/whoctl-sdk-go builds and tests with no workspace"

## fmt: format and vet
.PHONY: fmt
fmt:
	@gofmt -w .
	@go vet ./...

## help: list the targets
.PHONY: help
help:
	@echo "whoctl-sdk-go targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | awk -F': ' '{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
