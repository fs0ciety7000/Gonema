.PHONY: test bench lint build generate web release release-snapshot release-check docker openapi

test:
	go test ./...

bench:
	go test -run '^$$' -bench . -benchmem ./internal/...

lint:
	go tool -modfile=tools/go.mod golangci-lint run ./...

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GORELEASER ?= go run github.com/goreleaser/goreleaser/v2@v2.20.0

build:
	CGO_ENABLED=0 go build -trimpath -tags timetzdata -ldflags "-s -w -X main.version=$(VERSION)" -o bin/gonema ./cmd/gonema

generate:
	go tool -modfile=tools/go.mod sqlc generate

# web builds the SPA and copies it into internal/webui/dist, which `go build`
# embeds. Without it, the binary serves a placeholder page.
web:
	cd web && npm ci && npm run build
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
	cp -R web/dist/. internal/webui/dist/

# release is the binary of this machine with the web interface.
release: web build

# release-check validates .goreleaser.yaml; release-snapshot builds every
# archive and the multi-arch image locally (no publishing; needs Docker buildx
# and syft). A real release runs `goreleaser release --clean` from a tag.
release-check:
	$(GORELEASER) check

release-snapshot:
	$(GORELEASER) release --snapshot --clean

# docker builds the image of this machine's platform from the Dockerfile.
docker:
	docker build --build-arg VERSION=$(VERSION) -t gonema:$(VERSION) .

# openapi writes the API description the web client generates its types from.
openapi:
	go run ./cmd/gonema openapi > web/openapi.json
