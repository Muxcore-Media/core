FROM golang@sha256:87a41d2539e5671777734e91f467499ed5eafb1fb1f77221dff2744db7a51775 AS build

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

FROM alpine@sha256:5b10f432ef3da1b8d4c7eb6c487f2f5a8f096bc91145e68878dd4a5019afde11

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
