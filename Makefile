.PHONY: test cover cover-html fmt lint vulncheck smoke deadcode doc doc-all doc-serve

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

# PKG selects the package for doc and doc-all, e.g. make doc PKG=./internal/approval.
PKG ?= ./...
PKGSITE_VERSION ?= latest
DOC_ADDR ?= localhost:6060

# doc prints the exported API of PKG with go doc.
doc:
	@go list $(PKG) | xargs -n1 go doc -short

# doc-all prints the full documentation, including unexported declarations.
doc-all:
	@go list $(PKG) | xargs -n1 go doc -all -u

# doc-serve browses the module's documentation as HTML at DOC_ADDR.
doc-serve:
	@echo "Serving docs at http://$(DOC_ADDR)/github.com/alphaeron/kit"
	@go run golang.org/x/pkgsite/cmd/pkgsite@$(PKGSITE_VERSION) -http=$(DOC_ADDR) .
