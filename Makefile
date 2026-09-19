.PHONY: build test run demo
build:
	go build -o bin/husage .
test:
	go test -race ./...
run:
	go run .
demo:
	go run . --demo
