# syntax=docker/dockerfile:1

# ---- build ----
FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/pitbox ./cmd/pitbox

# ---- run ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/pitbox /pitbox
ENV HOST=0.0.0.0 PORT=8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/pitbox"]
