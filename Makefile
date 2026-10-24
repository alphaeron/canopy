.PHONY: test e2e cover cover-html fmt lint vulncheck smoke smoke-serve deadcode doc doc-all doc-serve docs release-check snapshot image local-up local-down tf-check

test:
	@go test -v ./...

cover:
	@go test -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1

cover-html: cover
	@go tool cover -html=coverage.out

# e2e runs each platform's end-to-end tests (internal/platform/<platform>/e2e):
# the review server as an app against an in-process fake of the platform.
e2e:
	@go test -race -count=1 ./internal/platform/.../e2e

smoke:
	@tmp=$$(mktemp -d) && trap "rm -rf $$tmp" EXIT && \
	go build -o $$tmp/canopy ./cmd/canopy && \
	scripts/smoke.sh $$tmp/canopy

smoke-serve:
	@tmp=$$(mktemp -d) && trap "rm -rf $$tmp" EXIT && \
	go build -o $$tmp/canopyweb ./cmd/canopyweb && \
	scripts/smoke-serve.sh $$tmp/canopyweb

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
	@echo "Serving docs at http://$(DOC_ADDR)/github.com/alphaeron/canopy"
	@go run golang.org/x/pkgsite/cmd/pkgsite@$(PKGSITE_VERSION) -http=$(DOC_ADDR) .

# docs builds the user documentation into build/docs (see internal/docgen):
# man pages, shell completions, and the site (open build/docs/site/index.html).
docs:
	@go run ./internal/docgen -out build/docs

# GoReleaser (see .goreleaser.yaml) builds the CLI's release artifacts.
GORELEASER_VERSION ?= latest
GORELEASER ?= go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)

# release-check validates .goreleaser.yaml.
release-check:
	@$(GORELEASER) check

# snapshot builds every release artifact into dist/ without publishing. The
# snap needs snapcraft, so it is skipped where snapcraft is not installed; the
# Windows and macOS installers are built only by the release workflow.
snapshot:
	@$(GORELEASER) release --snapshot --clean $(if $(shell command -v snapcraft),,--skip=snapcraft)

# Deployment of the review server (see deploy/README.md).
IMAGE ?= canopyweb:dev
PLATFORMS ?= linux/arm64

# image builds the canopyweb container image (Dockerfile) for PLATFORMS.
image:
	@docker buildx build --platform $(PLATFORMS) -t $(IMAGE) --load .

# local-up starts canopyweb, PostgreSQL, and LocalStack (deploy/local);
# local-down stops them and deletes their data.
local-up:
	@deploy/local/up.sh

local-down:
	@deploy/local/down.sh

# tf-check checks the Terraform's formatting and validates each environment
# without touching any state.
tf-check:
	@terraform fmt -check -recursive deploy/terraform
	@for env in deploy/terraform/envs/*/; do \
		terraform -chdir=$$env init -backend=false -input=false >/dev/null && \
		terraform -chdir=$$env validate || exit 1; \
	done
