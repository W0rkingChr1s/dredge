VERSION ?= 0.1.0
BIN     := dredge
DIST    := dist

# Installationsziel: PREFIX=$HOME/.local für eine Installation ohne root.
PREFIX  ?= /usr/local
DESTDIR ?=
BINDIR  := $(DESTDIR)$(PREFIX)/bin

# sudo verwirft $PATH, deshalb suchen wir go selbst statt uns darauf zu
# verlassen. Überschreibbar mit: make build GO=/pfad/zu/go
GO    ?= $(firstword $(shell command -v go 2>/dev/null) /usr/local/go/bin/go)
GOFMT ?= $(GO)fmt

LDFLAGS := -s -w -X main.version=$(VERSION)
GOBUILD  = CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)"

.PHONY: help build test vet fmt check install uninstall release docker clean check-go

help:
	@printf '%s\n' \
	  'dredge – make-Ziele:' \
	  '' \
	  '  build      Binary nach $(DIST)/$(BIN) bauen (kein sudo nötig)' \
	  '  install    Binary nach $(BINDIR) installieren (root nötig)' \
	  '  uninstall  Binary aus $(BINDIR) entfernen' \
	  '  test       Tests laufen lassen' \
	  '  check      gofmt + go vet + Tests' \
	  '  release    Cross-Builds + SHA256SUMS nach $(DIST)/' \
	  '  docker     Container-Image bauen' \
	  '  clean      $(DIST)/ löschen' \
	  '' \
	  'Ohne Go auf dem Host: ./install.sh lädt ein fertiges Release-Binary.'

# Freundliche Meldung statt "/bin/sh: 1: go: not found".
check-go:
	@command -v $(GO) >/dev/null 2>&1 || { \
	  printf '%s\n' \
	    'go wurde nicht gefunden ($(GO)).' \
	    '' \
	    '  * "make build" braucht kein sudo – sudo verwirft $$PATH.' \
	    '  * Ohne Go auf dem Host:  ./install.sh   (fertiges Release-Binary)' \
	    '  * Go liegt woanders:     make build GO=/pfad/zu/go' >&2; \
	  exit 1; }

build: check-go
	$(GOBUILD) -o $(DIST)/$(BIN) .
	@echo "✓ $(DIST)/$(BIN) – installieren mit: sudo make install"

test: check-go
	$(GO) test ./...

vet: check-go
	$(GO) vet ./...

fmt: check-go
	$(GO) fmt ./...

check: check-go
	@unformatted="$$($(GOFMT) -l .)"; \
	  [ -z "$$unformatted" ] || { echo "Nicht gofmt-konform:"; echo "$$unformatted"; exit 1; }
	$(GO) vet ./...
	$(GO) test ./...

install: build
	install -d $(BINDIR)
	install -m0755 $(DIST)/$(BIN) $(BINDIR)/$(BIN)
	@echo "✓ $(BINDIR)/$(BIN)"
	@echo "  Weiter: sudo $(BIN) setup   und dann   sudo $(BIN) install"

uninstall:
	rm -f $(BINDIR)/$(BIN)
	@echo "Binary entfernt. systemd-Units löscht 'sudo $(BIN) uninstall' (vorher ausführen)."

# Cross-Builds für gängige Homelab-Targets, identisch zu dem, was CI veröffentlicht.
release: check-go test
	mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(DIST)/$(BIN)-linux-amd64 .
	GOOS=linux GOARCH=arm64 $(GOBUILD) -o $(DIST)/$(BIN)-linux-arm64 .
	GOOS=linux GOARCH=arm GOARM=7 $(GOBUILD) -o $(DIST)/$(BIN)-linux-armv7 .
	cd $(DIST) && sha256sum $(BIN)-linux-* > SHA256SUMS

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(BIN):$(VERSION) .

clean:
	rm -rf $(DIST)
