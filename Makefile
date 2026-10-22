.PHONY: test cover cover-html fmt lint vulncheck smoke deadcode

test:
	@go test -v ./...

cover:
	@go test -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1

cover-html: cover
	@go tool cover -html=coverage.out

smoke:
	@tmp=$$(mktemp -d) && trap "rm -rf $$tmp" EXIT && \
	go build -o $$tmp/kit ./cmd/kit && \
	scripts/smoke.sh $$tmp/kit

fmt:
	@go fmt github.com/alphaeron/...

lint:
	golangci-lint run ./...

deadcode:
	@scripts/deadcode.sh

vulncheck:
	govulncheck -show verbose ./...
