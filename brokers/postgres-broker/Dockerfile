# syntax=docker/dockerfile:1.4

FROM golang:1.21-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /out/postgres-broker

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/postgres-broker /usr/local/bin/postgres-broker
ENV BROKER_LISTEN="unix:///tmp/postgres-broker.sock" \
    BROKER_UPSTREAM="/var/run/docker.sock" \
    BROKER_ALLOWED_IMAGE="postgres" \
    BROKER_ALLOWED_TAG="latest" \
    BROKER_ALLOW_PULLS="true"
ENTRYPOINT ["/usr/local/bin/postgres-broker"]
