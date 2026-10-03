BIN     := bin/hubtui
PKG     := ./cmd/hubtui
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

export CGO_ENABLED := 0

.PHONY: build run test lint fmt clean

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)

run:
	go run $(PKG) $(ARGS)

# -race needs cgo, so re-enable it just for tests; the shipped binary stays static.
test:
	CGO_ENABLED=1 go test ./... -race

lint:
	golangci-lint run

# golangci-lint's formatters run gofmt and goimports, so CI needs no extra tools.
fmt:
	golangci-lint fmt

clean:
	rm -rf bin dist
