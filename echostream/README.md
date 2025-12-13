# EchoStream 🌊

> A multi-tenant, Slack/Discord-style **real-time messaging backend** built with **Java 21, Spring Boot 3, WebSockets, Redis, and Aurora Postgres**, deployed on **AWS ECS Fargate** with **SQS-based fan-out, Redis for presence, full observability, and Terraform-managed infrastructure**.

## 🎯 Features

- **Multi-tenant workspaces** - Complete data isolation per tenant
- **Public & private channels** - Flexible channel management
- **Real-time messaging** - WebSocket-based instant message delivery
- **Typing indicators** - See when others are typing
- **Presence system** - Online/idle/offline status tracking
- **Message history** - Cursor-based pagination for message retrieval
- **Rate limiting** - Per-user and per-tenant throttling
- **Event-driven architecture** - SQS + Redis pub/sub for scalable fan-out

## 🏗 Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              EchoStream Architecture                         │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────┐     ┌─────────────────────────────────────────────┐            │
│  │ Clients │────▶│               ALB (WebSocket + REST)         │            │
│  └─────────┘     └─────────────────────────────────────────────┘            │
│                           │                                                  │
│                           ▼                                                  │
│                  ┌─────────────────┐                                        │
│                  │ Gateway Service │◀──────┐                                │
│                  │  (Spring Boot)  │       │                                │
│                  └────────┬────────┘       │                                │
│                           │                │ Redis Pub/Sub                  │
│                           │                │ (Real-time delivery)           │
│                           ▼                │                                │
│                  ┌─────────────────┐       │                                │
│                  │   SQS Queue     │       │                                │
│                  │ (Message Events)│       │                                │
│                  └────────┬────────┘       │                                │
│                           │                │                                │
│                           ▼                │                                │
│                  ┌─────────────────┐       │                                │
│                  │ Fanout Service  │───────┘                                │
│                  │  (SQS Consumer) │                                        │
│                  └────────┬────────┘                                        │
│                           │                                                  │
│                           ▼                                                  │
│  ┌─────────────────────────────────────────────────────────────────────────┐│
│  │                         Aurora PostgreSQL                                ││
│  │              (Tenants, Users, Channels, Messages)                        ││
│  └─────────────────────────────────────────────────────────────────────────┘│
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────────┐│
│  │                         ElastiCache Redis                                ││
│  │              (Pub/Sub, Presence, Typing, Rate Limiting)                  ││
│  └─────────────────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────────────────┘
```

## 📁 Project Structure

```
echostream/
├── backend/
│   ├── common/                 # Shared DTOs, events, utilities
│   ├── gateway-service/        # REST APIs + WebSocket handler
│   ├── fanout-service/         # SQS consumer, message persistence
│   ├── presence-service/       # (Future) Dedicated presence service
│   └── admin-service/          # (Future) Admin APIs
├── infra/
│   └── terraform/
│       ├── modules/            # Reusable Terraform modules
│       │   ├── vpc/
│       │   ├── rds_aurora/
│       │   ├── elasticache_redis/
│       │   ├── ecs_cluster/
│       │   ├── ecs_service/
│       │   ├── alb/
│       │   └── sqs/
│       └── envs/
│           ├── dev/
│           └── prod/
├── docker-compose.yml          # Local development setup
└── README.md
```

## 🚀 Quick Start

### Prerequisites

- Java 21
- Docker & Docker Compose
- Maven 3.9+

### Local Development

1. **Start infrastructure services:**
   ```bash
   docker-compose up -d postgres redis localstack
   ```

2. **Run the Gateway Service:**
   ```bash
   cd backend
   ./mvnw spring-boot:run -pl gateway-service
   ```

3. **Run the Fanout Service:**
   ```bash
   cd backend
   ./mvnw spring-boot:run -pl fanout-service
   ```

### Full Docker Setup

```bash
docker-compose up -d
```

This starts:
- PostgreSQL (port 5432)
- Redis (port 6379)
- LocalStack with SQS (port 4566)
- Gateway Service (port 8080)
- Fanout Service (port 8081)

## 📡 API Reference

### REST Endpoints

#### Tenant Management
```http
POST /api/v1/admin/tenants
Content-Type: application/json

{
  "name": "Acme Corp",
  "adminEmail": "admin@acme.com",
  "adminDisplayName": "Admin User"
}
```

#### Channels
```http
# Create channel
POST /api/v1/channels
Authorization: Bearer <token>

{ "name": "general", "isPrivate": false }

# List channels
GET /api/v1/channels

# Get channel
GET /api/v1/channels/{channelId}
```

#### Messages
```http
# Send message
POST /api/v1/channels/{channelId}/messages
Authorization: Bearer <token>

{ "body": "Hello, world!" }

# Get message history
GET /api/v1/channels/{channelId}/messages?before={messageId}&limit=50
```

### WebSocket

Connect to `/ws?token=<jwt_token>`

#### Client → Server
```json
// Subscribe to channel
{ "type": "subscribe", "channelId": "..." }

// Unsubscribe
{ "type": "unsubscribe", "channelId": "..." }

// Send message
{ "type": "send", "channelId": "...", "body": "Hello!" }

// Typing indicator
{ "type": "typing", "channelId": "..." }
```

#### Server → Client
```json
// New message
{
  "type": "message",
  "channelId": "...",
  "userId": "...",
  "body": "Hello!",
  "payload": { "messageId": 123, "senderDisplayName": "John", "createdAt": "..." }
}

// Presence change
{ "type": "presence", "userId": "...", "payload": { "status": "online" } }

// Typing indicator
{ "type": "typing_indicator", "channelId": "...", "userId": "...", "payload": { "displayName": "John" } }
```

## 🏛 Data Model

```sql
tenants (id, name, created_at, max_channels, max_messages_per_second)
users (id, tenant_id, email, display_name, created_at)
channels (id, tenant_id, name, is_private, created_at)
channel_members (channel_id, user_id, role, joined_at, last_read_at)
messages (id, tenant_id, channel_id, sender_id, body, created_at)
```

## ☁️ AWS Deployment

### Deploy with Terraform

```bash
cd infra/terraform/envs/dev

# Initialize
terraform init

# Plan
terraform plan -var="gateway_image=<ecr_image>" -var="fanout_image=<ecr_image>"

# Apply
terraform apply
```

### AWS Resources Created

- **VPC** with public, private, and database subnets
- **ECS Fargate** cluster with Gateway and Fanout services
- **Aurora PostgreSQL** for persistent storage
- **ElastiCache Redis** for pub/sub, presence, rate limiting
- **SQS** queues for message event processing
- **ALB** for load balancing WebSocket and REST traffic

## 📊 Observability

- **Metrics**: Micrometer → Prometheus/CloudWatch
- **Logging**: Structured JSON logging
- **Tracing**: OpenTelemetry → X-Ray (future)

Key metrics:
- `echostream.websocket.connections` - Active WebSocket connections
- `echostream.websocket.messages.broadcast` - Messages broadcast per channel
- `echostream.fanout.messages.processed` - Messages processed by fanout
- `echostream.fanout.processing.time` - Message processing latency

## 🔐 Security

- JWT-based authentication
- Per-tenant data isolation
- Rate limiting (per-user and per-tenant)
- Encrypted data at rest (RDS, Redis, S3)
- TLS for all connections

## 📈 Performance Targets

- **P95 Latency**: < 200ms from send to delivery
- **Throughput**: 10k+ concurrent WebSocket connections per Gateway
- **Message Rate**: 1000+ messages/second per tenant

## 🛣 Roadmap

- [ ] Direct messages (DMs)
- [ ] Message reactions
- [ ] File attachments (S3)
- [ ] Message search (OpenSearch)
- [ ] Push notifications
- [ ] Read receipts
- [ ] Audit logging

## 📝 License

MIT License
