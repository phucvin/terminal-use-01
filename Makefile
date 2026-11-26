.PHONY: build test clean fmt vet

BINARY := bin/terminal-use

build:
	@mkdir -p bin
	go build -o $(BINARY) ./cmd/terminal-use

test:
	go test -v -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -rf bin
