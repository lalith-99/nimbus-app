# Nimbus

Notification orchestration service in Go with PostgreSQL.

Provides CRUD API for managing notifications across email, SMS, and webhook channels.

## Quick Start

```bash
# Start the server
go run cmd/gateway/main.go

# Server starts on :8080
```

## Testing

### With Postman (Recommended)
```bash
# Import the collection
postman/Nimbus_API.postman_collection.json

# Import the environment
postman/Nimbus_Local.postman_environment.json

# See postman/README.md for detailed instructions
```

### With cURL
```bash
# Create notification
curl -X POST http://localhost:8080/v1/notifications \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": "00000000-0000-0000-0000-000000000001",
    "user_id": "00000000-0000-0000-0000-000000000002",
    "channel": "email",
    "payload": {"to": "user@example.com"}
  }'

# List notifications
curl "http://localhost:8080/v1/notifications?tenant_id=00000000-0000-0000-0000-000000000001"
```

## Features

- Complete CRUD API (create, get, list, update status)
- PostgreSQL with connection pooling
- Repository pattern for database access
- Structured logging with zap
- Request validation and error handling
- 92% test coverage with 26 unit tests

## API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/health` | Health check |
| POST | `/v1/notifications` | Create notification |
| GET | `/v1/notifications` | List notifications |
| GET | `/v1/notifications/{id}` | Get notification |
| PATCH | `/v1/notifications/{id}/status` | Update status |

See [docs/API.md](docs/API.md) for detailed documentation.

## Documentation

- [API Reference](docs/API.md) - Complete API documentation
- [Postman Guide](postman/README.md) - Testing with Postman collection
- [Architecture](docs/MIGRATION_COMPLETE.md) - System design details

## Project Structure

```
nimbus/
├── cmd/gateway/          # Main application entry point
├── internal/
│   ├── api/             # HTTP handlers
│   ├── db/              # Database layer (Repository pattern)
│   ├── config/          # Configuration management
│   └── observ/          # Observability (logging)
├── migrations/          # Database migrations
├── docs/               # Documentation
└── postman/            # Postman collection for testing
```
