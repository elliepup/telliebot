# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/telliebot ./cmd/telliebot

# distroless/static ships CA certs (needed for HTTPS) and runs as non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/telliebot /telliebot
ENTRYPOINT ["/telliebot"]
