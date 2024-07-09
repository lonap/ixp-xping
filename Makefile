PKG    = github.com/benjojo/ixp-xping
PREFIX = /usr

all: build/ixp-xping

# NOTE: This repo uses Go modules, and uses a synthetic GOPATH at
# $(CURDIR)/.gopath that is only used for the build cache. $GOPATH/src/ is
# empty.
GO            = GOPATH=$(CURDIR)/.gopath GOBIN=$(CURDIR)/build go
GO_BUILDFLAGS =
GO_LDFLAGS    = -s -w

build/ixp-xping: *.go
	$(GO) install $(GO_BUILDFLAGS) -ldflags "$(GO_LDFLAGS)" .

install: build/ixp-xping
	install -D -m 0755 build/ixp-xping "$(DESTDIR)$(PREFIX)/bin/ixp-xping"

vendor:
	$(GO) mod tidy
	$(GO) mod vendor

.PHONY: install vendor