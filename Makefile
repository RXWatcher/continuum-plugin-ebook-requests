BINARY := continuum-plugin-ebookdb
GO ?= go

.PHONY: build test clean
build:
	$(GO) build -o $(BINARY) ./cmd/continuum-plugin-ebookdb
test:
	$(GO) test ./...
clean:
	rm -f $(BINARY)
