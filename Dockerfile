# syntax=docker/dockerfile:1
# Image for a Forge example service (examples/basic-api by default). Generated
# applications get their own Dockerfile from `forge new`; this one mirrors it.
#
#   docker build -t forge-example .
#   docker build --build-arg EXAMPLE=basic-api -t forge-example .

ARG GO_VERSION=1.27

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0
COPY . .
ARG EXAMPLE=basic-api
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/server ./examples/${EXAMPLE}

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/server /app/server
ENV FORGE_ENV=production \
    FORGE_HTTP_ADDR=0.0.0.0:8080 \
    FORGE_HTTP_TRANSPORT=trusted-proxy
EXPOSE 8080
USER nonroot:nonroot
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD ["/app/server", "healthcheck"]
ENTRYPOINT ["/app/server"]
CMD ["serve"]
