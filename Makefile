.PHONY: build install test race vet fmt ci darwin-build clean

GO ?= go
INSTALL ?= install
BIN_DIR := bin
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
DESTDIR ?=
VERSION := $(shell cat VERSION)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -X github.com/jimmingcheng/safe-imsg/internal/version.Version=$(VERSION) -X github.com/jimmingcheng/safe-imsg/internal/version.Commit=$(COMMIT)

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/safe-imsg ./cmd/safe-imsg
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/safe-imsgd ./cmd/safe-imsgd

install: build
	$(INSTALL) -d $(DESTDIR)$(BINDIR)
	$(INSTALL) -m 0755 $(BIN_DIR)/safe-imsg $(DESTDIR)$(BINDIR)/safe-imsg
	$(INSTALL) -m 0755 $(BIN_DIR)/safe-imsgd $(DESTDIR)$(BINDIR)/safe-imsgd

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w cmd internal

darwin-build:
	GOOS=darwin GOARCH=arm64 $(GO) build ./cmd/safe-imsg ./cmd/safe-imsgd

ci: test race vet build darwin-build

clean:
	rm -f $(BIN_DIR)/safe-imsg $(BIN_DIR)/safe-imsgd
