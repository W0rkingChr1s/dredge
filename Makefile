VERSION ?= 0.1.0
LDFLAGS := -s -w -X main.version=$(VERSION)
BIN := dredge

.PHONY: build test vet clean release docker run

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BIN) .

test:
	go test ./...

vet:
	go vet ./...

# Cross-compile für gängige Homelab-Targets.
release: test
	mkdir -p dist
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BIN)-linux-amd64 .
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BIN)-linux-arm64 .
	GOOS=linux GOARCH=arm   CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BIN)-linux-armv7 .

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(BIN):$(VERSION) .

clean:
	rm -rf dist
