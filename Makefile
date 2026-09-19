BINARY := aven
VERSION ?= dev

.PHONY: build build-linux test vet clean

build:
	go build -o $(BINARY) .

# Linux release binaries (pure Go, no CGO)
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/aven-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o dist/aven-linux-arm64 .

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf dist $(BINARY)
