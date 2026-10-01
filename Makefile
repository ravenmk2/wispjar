.PHONY: lint test build

lint:
	golangci-lint run

test:
	CGO_ENABLED=0 go test ./...

build:
	./scripts/build.sh
