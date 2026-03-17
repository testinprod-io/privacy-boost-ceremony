FROM golang:1.24.0-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/ceremony ./cmd/ceremony

FROM debian:bookworm-slim
WORKDIR /work
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ceremony /usr/local/bin/ceremony
ENTRYPOINT ["ceremony"]
