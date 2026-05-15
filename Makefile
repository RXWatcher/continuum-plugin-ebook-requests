BINARY := continuum-plugin-annas-archive-downloader
GO ?= go

.PHONY: build test clean
build:
	$(GO) build -o $(BINARY) ./cmd/continuum-plugin-annas-archive-downloader
test:
	$(GO) test ./...
clean:
	rm -f $(BINARY)
