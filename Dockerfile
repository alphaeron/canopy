# Image of canopyweb, the review server (see docs/server.md and deploy/).
# The build runs on the build platform and cross-compiles for the target, so
# multi-arch builds need no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
	go build -trimpath -ldflags='-s -w' -o /out/canopyweb ./cmd/canopyweb

# RDS's certificate authorities are not in the system roots; the deployment's
# database_url names this bundle (sslrootcert) to verify the database.
FROM --platform=$BUILDPLATFORM alpine:3.24 AS rds-ca
ADD https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem /rds-ca.pem

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/canopyweb /usr/local/bin/canopyweb
COPY --from=rds-ca /rds-ca.pem /etc/ssl/certs/rds-ca.pem
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/canopyweb"]
