# Cross-compiles on the build machine and ends in scratch, so an arm64 image
# builds on a plain x86 runner without QEMU (the last stage has no RUN).
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine AS builder

ENV GOTOOLCHAIN=local
RUN apk add --no-cache ca-certificates

WORKDIR /build
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . ./
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o runner ./cmd/runner

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /build/runner /usr/local/bin/runner
# The Sustainable Use License must travel with every copy.
COPY LICENSE /usr/share/doc/runscaler/LICENSE
ENTRYPOINT ["/usr/local/bin/runner", "run"]
