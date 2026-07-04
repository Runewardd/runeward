# runeward control plane, MCP server, and Kubernetes controller (one binary).
FROM --platform=$BUILDPLATFORM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -ldflags "-s -w -X github.com/adefemi171/runeward/internal/cli.version=${VERSION}" \
    -o /out/runeward ./cmd/runeward

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/adefemi171/runeward"
LABEL org.opencontainers.image.description="Governed execution cells for AI agents"
COPY --from=build /out/runeward /usr/local/bin/runeward
ENTRYPOINT ["/usr/local/bin/runeward"]
CMD ["serve", "--port", "8080"]
