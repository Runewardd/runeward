# Build the main runeward binary (control plane, MCP server, and Kubernetes
# controller are all subcommands of this one binary).
#
#   docker build -f deploy/Dockerfile -t runeward:latest .
#
# On OrbStack/Docker Desktop the image is shared with the built-in Kubernetes,
# so `runeward up` / the Helm chart can use it without a push. For remote
# clusters, push to a registry and pass that reference.

FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X github.com/adefemi171/runeward/internal/cli.version=${VERSION}" \
    -o /out/runeward ./cmd/runeward

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/adefemi171/runeward"
LABEL org.opencontainers.image.description="Governed execution cells for AI agents"
COPY --from=build /out/runeward /usr/local/bin/runeward
ENTRYPOINT ["/usr/local/bin/runeward"]
CMD ["serve", "--port", "8080"]
