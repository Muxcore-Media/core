FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /muxcored ./cmd/muxcored

FROM alpine:3.22

RUN adduser -D -h /app muxcore
WORKDIR /app
COPY --from=build /muxcored .

USER muxcore
EXPOSE 8080

ENTRYPOINT ["./muxcored"]
