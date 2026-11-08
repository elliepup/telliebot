# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/telliebot ./cmd/telliebot \
 && mkdir -p /out/data

# distroless/static ships CA certs (needed for HTTPS) and runs as non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/telliebot /telliebot
# The database lives on a volume at /data. Creating the directory here, owned
# by the nonroot user (65532), gives a fresh named volume the same ownership.
COPY --from=build --chown=65532:65532 /out/data /data
ENV DB_PATH=/data/telliebot.db
ENTRYPOINT ["/telliebot"]
