VERSION ?= dev
LDFLAGS = -s -w -X main.version=$(VERSION)

.PHONY: build install test clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o sooda-mcp .

install:
	CGO_ENABLED=0 go install -ldflags "$(LDFLAGS)" .

test:
	go vet ./...

clean:
	rm -f sooda-mcp
