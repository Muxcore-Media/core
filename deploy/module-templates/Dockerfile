# Stage 1: Build the module binary
# TODO: replace module path and binary name
FROM golang@sha256:68cb6d68bed024785b69195b89af7ac7a444f27791435f98647edff595aa0479 AS build

ARG VERSION=0.0.0-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /module \
    ./cmd/module/

# Stage 2: Minimal runtime
FROM alpine@sha256:5b10f432ef3da1b8d4c7eb6c487f2f5a8f096bc91145e68878dd4a5019afde11

RUN apk add --no-cache ca-certificates
RUN adduser -D -h /app muxcore
WORKDIR /app
COPY --from=build /module .

USER muxcore
# TODO: set the correct port for the module's health/HTTP endpoint
# EXPOSE 9100

ENTRYPOINT ["./module"]
