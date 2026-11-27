.PHONY: build test lint serve-example

build:
	go build -o docsbot ./cmd/docsbot

test:
	go test ./...

lint:
	gofmt -l . | (! grep .) && go vet ./...

# Serve the test fixture as a live site on :8484 (handy for trying live mode and afdocs).
serve-example: build
	./docsbot serve testdata/site
