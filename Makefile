SHELL := /bin/sh
GO     := go
MODULE := xray-exporter
NAME   := xray-exporter
VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
DIST    := dist

.PHONY: all build test fmt lint check image clean help

all: build

help:
	@grep -E '^[a-zA-Z_-]+:.*? ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*? "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

build:
	@mkdir -p $(DIST)
	@CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -trimpath \
		-ldflags "-s -w -X main.buildVersion=$(VERSION) \
		  -X main.buildCommit=$(COMMIT) \
		  -X main.buildDate=$(DATE)" \
		-o $(DIST)/$(NAME) .

test:
	@$(GO) test -v -race -count=1 ./...

vet:
	@$(GO) vet ./...

fmt:
	@$(GO) fmt ./...

lint: vet

check: lint fmt test

image:
	@docker build -t $(NAME):$(VERSION) .

clean:
	@rm -rf $(DIST)
