BINARY := vbcdd-harmony
PREFIX ?= /usr/local

.PHONY: all build test vet fmt install clean

all: build

build:
	go build -o $(BINARY) .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

install: build
	install -m 0755 $(BINARY) $(PREFIX)/bin/$(BINARY)
	install -m 0755 deploy/vbcdd-sandbox $(PREFIX)/bin/vbcdd-sandbox

clean:
	rm -f $(BINARY)
