# syntax=docker/dockerfile:1

FROM golang:1.27.2-alpine AS build

WORKDIR /src

# Download dependencies in a cache-friendly layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Validate every package and produce a compiled artifact during the image build.
RUN go test ./... \
    && go build -trimpath -o /out/aiconnectionmanager .

FROM golang:1.27.2-alpine AS runtime

# Keep tini aligned with the Alpine package repository used by the base image.
# hadolint ignore=DL3018
RUN apk add --no-cache tini \
    && addgroup -S app \
    && adduser -S -G app -u 10001 app

WORKDIR /app

# go run is intentionally used at startup, so the source and module cache are
# included in the runtime image. The build stage above still catches compile
# and test failures before this image can be produced.
COPY --from=build --chown=app:app /src/ ./
COPY --from=build /go/pkg/mod/ /go/pkg/mod/

RUN mkdir -p /app/configs /tmp/go-build /tmp/go-tmp \
    && chown -R app:app /app /tmp/go-build /tmp/go-tmp

ENV GOCACHE=/tmp/go-build \
    GOMODCACHE=/go/pkg/mod \
    GOTMPDIR=/tmp/go-tmp \
    GOTOOLCHAIN=local \
    GOPROXY=off

USER app

EXPOSE 8443

# Bind to all interfaces so Docker Desktop can publish the service to the host.
ENTRYPOINT ["/sbin/tini", "--", "go", "run", ".", "server", "-addr", "0.0.0.0:8443"]
