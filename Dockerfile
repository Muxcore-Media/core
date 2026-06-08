FROM golang@sha256:91eda9776261207ea25fd06b5b7fed8d397dd2c0a283e77f2ab6e91bfa71079d AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -tags default -ldflags="-s -w" -o /muxcored ./cmd/muxcored

FROM alpine@sha256:5b10f432ef3da1b8d4c7eb6c487f2f5a8f096bc91145e68878dd4a5019afde11

RUN apk add --no-cache curl ca-certificates

RUN adduser -D -h /app muxcore
WORKDIR /app
COPY --from=build /muxcored .

USER muxcore
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["curl", "-sf", "http://127.0.0.1:8080/health"]

ENTRYPOINT ["./muxcored"]
