# syntax=docker/dockerfile:1

# ---- build ----
# Runs on the build machine's platform and cross-compiles to the target's
# (CI publishes linux/amd64), so building on another machine needs no
# emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
ARG VERSION=docker
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/pitbox ./cmd/pitbox

# ---- run ----
# Photos are shrunk by the libvips command line tools, so the image is a
# slim Debian with libvips-tools instead of distroless. libheif's HEVC
# decoder (HEIC photos) is only a recommendation, so it's named here; the
# JPEG, PNG, WebP and GIF loaders are part of libvips.
FROM debian:trixie-slim
LABEL org.opencontainers.image.title="Pitbox" \
      org.opencontainers.image.description="Garage stock, service kits and shopping list for your vehicles" \
      org.opencontainers.image.source="https://github.com/sunny-azu-red/stock-items" \
      org.opencontainers.image.licenses="MIT"
RUN apt-get update \
 && apt-get install -y --no-install-recommends libvips-tools libheif-plugin-libde265 ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/* /var/cache/apt/archives/* /var/log/apt /var/log/dpkg.log \
 # A file, not a symlink into zoneinfo: compose mounts the host's
 # /etc/localtime over it, which must not replace the UTC zone file.
 && cp --remove-destination /usr/share/zoneinfo/Etc/UTC /etc/localtime \
 && groupadd --system --gid 65532 nonroot \
 && useradd --system --uid 65532 --gid nonroot --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin nonroot \
 && vips --version
COPY --from=build /out/pitbox /pitbox
ENV HOST=0.0.0.0 PORT=8080
EXPOSE 8080
USER nonroot:nonroot
# The binary checks its own /healthz (no curl needed).
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --start-interval=2s --retries=3 \
    CMD ["/pitbox", "-healthcheck"]
ENTRYPOINT ["/pitbox"]
