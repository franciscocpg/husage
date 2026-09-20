.PHONY: build lint test run demo release-check release-snapshot
build:
	go build -o bin/husage .
lint:
	@files=$$(gofmt -l .) || exit 1; \
	if [ -n "$$files" ]; then \
		printf 'Run gofmt on these files:\n%s\n' "$$files"; \
		exit 1; \
	fi
	go vet ./...
test:
	go test -race ./...
run:
	go run .
demo:
	go run . --demo
release-check:
	goreleaser check
release-snapshot:
	goreleaser release --snapshot --clean
