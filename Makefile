.PHONY: test fmt lint vulncheck smoke

test:
	@go test -v ./...

smoke:
	@tmp=$$(mktemp -d) && trap "rm -rf $$tmp" EXIT && \
	go build -o $$tmp/kit ./cmd/kit && \
	scripts/smoke.sh $$tmp/kit

fmt:
	@go fmt github.com/alphaeron/...

lint:
	golangci-lint run ./...

vulncheck:
	govulncheck -show verbose ./...
