# Build
FROM golang:1.23-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /uts_bot .

# Runtime: no browser — SAIA sync uses HTTP + HTML parsing only
FROM debian:bookworm-slim
RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates \
	&& rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=build /uts_bot /app/uts_bot

ENV API_LISTEN=:8080

EXPOSE 8080
ENTRYPOINT ["/app/uts_bot"]
