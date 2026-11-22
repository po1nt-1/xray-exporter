SHELL   := /bin/sh
GO      ?= go
NAME    := xray-exporter
VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
DIST    := dist
XRAY_BIN ?= $(shell command -v xray)
PLATFORMS ?= linux/amd64 linux/arm64 windows/amd64

LDFLAGS := -s -w -X main.buildVersion=$(VERSION) -X main.buildCommit=$(COMMIT) -X main.buildDate=$(DATE)

.PHONY: all build dist test integration vet lint fmt check image clean

all: build

build:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(NAME) .

dist:
	@mkdir -p $(DIST)
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ "$$os" = windows ] && ext=.exe; \
		echo "$$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
			-o $(DIST)/$(NAME)-$$os-$$arch$$ext .; \
	done
	@cd $(DIST) && sha256sum $(NAME)-* > checksums.txt

test:
	$(GO) test -race -count=1 ./...

integration:
	@test -n "$(XRAY_BIN)" || { echo "set XRAY_BIN to an xray binary"; exit 1; }
	XRAY_BIN=$(XRAY_BIN) $(GO) test -tags integration -race -count=1 -run Integration ./...

vet:
	$(GO) vet ./...
	$(GO) vet -tags integration ./...

lint: vet
	golangci-lint run ./...
	golangci-lint run --build-tags integration ./...

fmt:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }

check: fmt lint test

image:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(NAME):$(VERSION) .

clean:
	rm -rf $(DIST)
