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
FROM gcr.io/distroless/static-debian13:nonroot
LABEL org.opencontainers.image.title="Pitbox" \
      org.opencontainers.image.description="Garage stock, service kits and shopping list for your vehicles" \
      org.opencontainers.image.source="https://github.com/sunny-azu-red/stock-items" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/pitbox /pitbox
ENV HOST=0.0.0.0 PORT=8080
EXPOSE 8080
USER nonroot:nonroot
# No shell or curl in distroless: the binary checks its own /healthz.
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --start-interval=2s --retries=3 \
    CMD ["/pitbox", "-healthcheck"]
ENTRYPOINT ["/pitbox"]
