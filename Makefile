.PHONY: test bench lint build generate web release openapi

test:
	go test ./...

bench:
	go test -run '^$$' -bench . -benchmem ./internal/...

lint:
	go tool -modfile=tools/go.mod golangci-lint run ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/gonema ./cmd/gonema

generate:
	go tool -modfile=tools/go.mod sqlc generate

# web builds the SPA and copies it into internal/webui/dist, which `go build`
# embeds. Without it, the binary serves a placeholder page.
web:
	cd web && npm ci && npm run build
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
	cp -R web/dist/. internal/webui/dist/

# release is the binary with the web interface.
release: web build

# openapi writes the API description the web client generates its types from.
openapi:
	go run ./cmd/gonema openapi > web/openapi.json
