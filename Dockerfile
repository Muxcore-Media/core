FROM golang@sha256:4013ae0f9e7994f8535c58c811f8f863fbed38b72e0d51e6592156f758d66146 AS build

# VERSION is injected at docker build time:
#   docker build --build-arg VERSION=1.0.0 -t muxcore:1.0.0 .
# Falls back to "0.0.0-dev" when not supplied.
ARG VERSION=0.0.0-dev

WORKDIR /src
COPY go.mod go.sum ./
COPY pkg/contracts/go.mod ./pkg/contracts/
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X github.com/Muxcore-Media/core/internal/version.Version=${VERSION}" \
    -o /muxcored \
    ./cmd/muxcored

FROM alpine@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

RUN apk add --no-cache curl ca-certificates

RUN adduser -D -h /app muxcore
WORKDIR /app
# Create writable directories for read-only root filesystem compatibility.
RUN mkdir -p /app/data /app/tmp && chown muxcore /app/data /app/tmp
COPY --from=build /muxcored .

USER muxcore
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["sh", "-c", "curl -sf https://127.0.0.1:8080/health || curl -sf http://127.0.0.1:8080/health"]

ENTRYPOINT ["./muxcored"]
