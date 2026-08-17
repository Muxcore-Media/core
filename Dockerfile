FROM golang@sha256:0d1d3a794be25f809dd2cb3160d8c73276c4056a9f8242a138e908ddeee7b6b6 AS build

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

FROM alpine@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

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
