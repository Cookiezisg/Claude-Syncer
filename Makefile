VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build test lint dist clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o claude-syncer ./cmd/claude-syncer

test:
	go test -race ./...

lint:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...

# Universal (Apple Silicon + Intel) release archive in dist/.
dist: clean
	mkdir -p dist
	GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/claude-syncer-arm64 ./cmd/claude-syncer
	GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/claude-syncer-amd64 ./cmd/claude-syncer
	lipo -create -output dist/claude-syncer dist/claude-syncer-arm64 dist/claude-syncer-amd64
	rm dist/claude-syncer-arm64 dist/claude-syncer-amd64
	tar -C dist -czf dist/claude-syncer-$(VERSION)-darwin-universal.tar.gz claude-syncer
	cd dist && shasum -a 256 *.tar.gz > checksums.txt

clean:
	rm -rf dist claude-syncer
