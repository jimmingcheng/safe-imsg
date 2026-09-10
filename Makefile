.PHONY: build install test race vet fmt ci darwin-build macos-contacts macos-test clean

GO ?= go
INSTALL ?= install
BIN_DIR := bin
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
DESTDIR ?=
VERSION := $(shell cat VERSION)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -X github.com/jimmingcheng/safe-imsg/internal/version.Version=$(VERSION) -X github.com/jimmingcheng/safe-imsg/internal/version.Commit=$(COMMIT)
CONTACTS_APP := $(BIN_DIR)/Safe Imsg Contacts.app
CODESIGN_IDENTITY ?= -

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
	GOOS=darwin GOARCH=amd64 $(GO) build ./cmd/safe-imsg ./cmd/safe-imsgd

# Run on macOS with Command Line Tools. A stable signing identity is recommended
# for releases so Contacts permission can survive updates. No permission prompt
# occurs during build or synthetic tests.
macos-contacts:
	test "$$(uname -s)" = Darwin
	mkdir -p "$(CONTACTS_APP)/Contents/MacOS"
	$(INSTALL) -m 0644 macos/Info.plist "$(CONTACTS_APP)/Contents/Info.plist"
	xcrun swiftc -O -framework Contacts -framework AppKit macos/ContactsCore.swift macos/ContactsTransport.swift macos/main.swift -o "$(CONTACTS_APP)/Contents/MacOS/safe-imsg-contacts"
	codesign --force --sign "$(CODESIGN_IDENTITY)" --identifier com.safe-imsg.contacts "$(CONTACTS_APP)"

macos-test:
	test "$$(uname -s)" = Darwin
	mkdir -p $(BIN_DIR)
	xcrun swiftc macos/ContactsCore.swift macos/ContactsCoreTests.swift -o $(BIN_DIR)/contacts-core-tests
	$(BIN_DIR)/contacts-core-tests
	xcrun swiftc macos/ContactsCore.swift macos/ContactsTransport.swift macos/ContactsTransportTests.swift -o $(BIN_DIR)/contacts-transport-tests
	$(BIN_DIR)/contacts-transport-tests

ci: test race vet build darwin-build

clean:
	rm -f $(BIN_DIR)/safe-imsg $(BIN_DIR)/safe-imsgd
