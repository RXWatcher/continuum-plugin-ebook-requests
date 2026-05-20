BINARY := continuum-plugin-ebook-requests
GO ?= go

.PHONY: build test clean
build:
	$(GO) build -o $(BINARY) ./cmd/continuum-plugin-ebook-requests
test:
	$(GO) test ./...
clean:
	rm -f $(BINARY)
