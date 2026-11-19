# The build stage always runs on the native build platform and cross-compiles,
# so multi-arch builds don't need slow QEMU emulation.
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build
ARG TARGETOS TARGETARCH BUILDARCH
# CGO is needed for go-sqlite3: install a C cross-compiler + libc for the target
# (the image's native gcc is used when target == build arch).
RUN set -eux; \
    if [ "$TARGETARCH" != "$BUILDARCH" ]; then \
      case "$TARGETARCH" in \
        amd64) pkgs="gcc-x86-64-linux-gnu libc6-dev-amd64-cross" ;; \
        arm64) pkgs="gcc-aarch64-linux-gnu libc6-dev-arm64-cross" ;; \
        *) echo "unsupported arch $TARGETARCH"; exit 1 ;; \
      esac; \
      apt-get update && apt-get install -y --no-install-recommends $pkgs && rm -rf /var/lib/apt/lists/*; \
    fi
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# goolm is the pure-Go olm implementation (no libolm needed).
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    set -eux; \
    if [ "$TARGETARCH" != "$BUILDARCH" ]; then \
      case "$TARGETARCH" in \
        amd64) export CC=x86_64-linux-gnu-gcc ;; \
        arm64) export CC=aarch64-linux-gnu-gcc ;; \
      esac; \
    fi; \
    CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH \
      go build -tags goolm -trimpath -ldflags="-s -w" -o /out/matrix-youtube-preview .

FROM gcr.io/distroless/base-debian12:nonroot
COPY --from=build /out/matrix-youtube-preview /usr/local/bin/matrix-youtube-preview
WORKDIR /data
VOLUME /data
ENTRYPOINT ["/usr/local/bin/matrix-youtube-preview"]
CMD ["-config", "/config/config.yaml"]
