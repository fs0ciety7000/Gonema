.PHONY: test bench lint build

test:
	go test ./...

bench:
	go test -run '^$$' -bench . -benchmem ./internal/...

lint:
	go tool -modfile=tools/go.mod golangci-lint run ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/gonema ./cmd/gonema
