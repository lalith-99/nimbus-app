FROM golang:1.23-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/gateway ./cmd/gateway
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/migrator ./cmd/migrator

FROM alpine:3.19

WORKDIR /app

RUN apk --no-cache add ca-certificates

COPY --from=builder /app/bin/gateway /app/gateway
COPY --from=builder /app/bin/migrator /app/migrator
COPY migrations /app/migrations

EXPOSE 8080

# Entrypoint script that runs migrations then starts the app
RUN cat > /app/entrypoint.sh << 'EOF'
#!/bin/sh
set -e
echo "Running migrations..."
MIGRATIONS_DIR=/app/migrations /app/migrator
echo "Starting gateway..."
exec /app/gateway
EOF

RUN chmod +x /app/entrypoint.sh

ENTRYPOINT [ "/app/entrypoint.sh" ]