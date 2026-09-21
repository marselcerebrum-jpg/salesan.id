# syntax=docker/dockerfile:1

# --- build -------------------------------------------------------------------
FROM golang:1.27-alpine AS builder

WORKDIR /src

# Cache dependencies separately from source so edits do not re-download modules.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO is not needed: pgx and lib/pq are pure Go.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

# --- runtime -----------------------------------------------------------------
FROM alpine:3.22

# ca-certificates for TLS to Supabase and WhatsApp; tzdata for correct
# timestamps in the inbox.
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 10001 salesan

WORKDIR /app
COPY --from=builder /out/server /app/server
# Migrations are run on purpose, before a new server starts, never by the
# server itself: `docker compose run --rm backend /app/migrate`.
COPY --from=builder /out/migrate /app/migrate
COPY --from=builder /src/migrations /app/migrations

USER salesan
EXPOSE 8080

# The server handles SIGTERM: it stops accepting requests, closes every
# whatsmeow socket, then drains the realtime hub.
STOPSIGNAL SIGTERM

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/health || exit 1

ENTRYPOINT ["/app/server"]
