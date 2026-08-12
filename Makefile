.PHONY: generate test lint build check

generate:
	buf generate

test:
	go test -buildvcs=false ./...

lint:
	buf lint

build:
	go build -buildvcs=false -o bgpls ./cmd/bgpls

check: lint test build
