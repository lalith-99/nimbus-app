# Nimbus Architecture Diagrams

## Current Architecture (v1.0)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              NIMBUS GATEWAY                                  │
│                            (Single Process)                                  │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                          HTTP Server (Chi)                              │ │
│  │                            Port 8080                                    │ │
│  │                                                                         │ │
│  │   Middleware: RequestID, RealIP, Recoverer, Timeout(30s), Logging      │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                     │                                        │
│                                     ▼                                        │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                           API Handlers                                  │ │
│  │                                                                         │ │
│  │   Notifications:                           Dead Letter Queue:           │ │
│  │   ├─ POST /v1/notifications                ├─ GET  /v1/dlq              │ │
│  │   ├─ GET  /v1/notifications                ├─ GET  /v1/dlq/{id}         │ │
│  │   ├─ GET  /v1/notifications/{id}           ├─ POST /v1/dlq/{id}/retry   │ │
│  │   └─ PATCH /v1/notifications/{id}/status   └─ POST /v1/dlq/{id}/discard │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                     │                                        │
│                                     ▼                                        │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                        Repository Layer                                 │ │
│  │                   (Interface-based abstraction)                         │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                     │                                        │
│          ┌──────────────────────────┴──────────────────────────┐            │
│          │                                                      │            │
│          ▼                                                      ▼            │
│   ┌─────────────────┐                                  ┌─────────────────┐  │
│   │  notifications  │                                  │  dead_letter_   │  │
│   │     table       │                                  │  notifications  │  │
│   │                 │                                  │                 │  │
│   │ - id            │                                  │ - id            │  │
│   │ - tenant_id     │◀─────── original_notification_id │ - tenant_id     │  │
│   │ - user_id       │                                  │ - attempts      │  │
│   │ - channel       │                                  │ - last_error    │  │
│   │ - payload       │                                  │ - status        │  │
│   │ - status        │                                  │   (pending/     │  │
│   │ - attempt       │                                  │    retried/     │  │
│   │ - next_retry_at │                                  │    discarded)   │  │
│   └─────────────────┘                                  └─────────────────┘  │
│                                                                              │
└──────────────────────────────────────┬───────────────────────────────────────┘
                                       │
                                       │ Same Process (goroutine)
                                       │
┌──────────────────────────────────────▼───────────────────────────────────────┐
│                            BACKGROUND WORKER                                  │
│                                                                               │
│   ┌─────────────────────────────────────────────────────────────────────┐    │
│   │                         Poll Loop (5s interval)                      │    │
│   │                                                                      │    │
│   │   for {                                                              │    │
│   │       notifications := repo.GetPendingNotifications(limit: 10)       │    │
│   │       for _, n := range notifications {                              │    │
│   │           processNotification(n)                                     │    │
│   │       }                                                              │    │
│   │       time.Sleep(5 * time.Second)                                    │    │
│   │   }                                                                  │    │
│   └─────────────────────────────────────────────────────────────────────┘    │
│                                      │                                        │
│                                      ▼                                        │
│   ┌─────────────────────────────────────────────────────────────────────┐    │
│   │                    processNotification(notif)                        │    │
│   │                                                                      │    │
│   │   1. Mark as "processing"                                            │    │
│   │   2. sender.Send(notif)                                              │    │
│   │   3. If success → status = "sent"                                    │    │
│   │      If failure:                                                     │    │
│   │        - attempt < maxRetries → status = "pending" + backoff         │    │
│   │        - attempt >= maxRetries → MoveToDeadLetter()                  │    │
│   └─────────────────────────────────────────────────────────────────────┘    │
│                                      │                                        │
│                                      ▼                                        │
│   ┌─────────────────────────────────────────────────────────────────────┐    │
│   │                          SES Sender                                  │    │
│   │                        (AWS SDK v2)                                  │    │
│   └─────────────────────────────────────────────────────────────────────┘    │
│                                      │                                        │
└──────────────────────────────────────┼────────────────────────────────────────┘
                                       │
                                       ▼
                            ┌─────────────────────┐
                            │      AWS SES        │
                            │   (Email Delivery)  │
                            └─────────────────────┘


┌─────────────────────────────────────────────────────────────────────────────┐
│                               EXTERNAL                                       │
│                                                                              │
│   ┌───────────────────────────────────────────────────────────────────┐     │
│   │                         PostgreSQL 15                              │     │
│   │                                                                    │     │
│   │   Database: nimbus                                                 │     │
│   │   Tables: notifications, dead_letter_notifications                 │     │
│   │   Connection: pgxpool (10 min, 4 max connections)                  │     │
│   └───────────────────────────────────────────────────────────────────┘     │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Current Data Flow

```
                    API Request                          Background Worker
                         │                                      │
    ┌────────────────────▼────────────────────┐                │
    │          POST /v1/notifications          │                │
    │                                          │                │
    │  {                                       │                │
    │    "tenant_id": "uuid",                  │                │
    │    "user_id": "uuid",                    │                │
    │    "channel": "email",                   │                │
    │    "payload": {"to": "...", ...}         │                │
    │  }                                       │                │
    └────────────────────┬────────────────────┘                │
                         │                                      │
                         ▼                                      │
                  ┌─────────────┐                               │
                  │  Validate   │                               │
                  │  - UUIDs    │                               │
                  │  - channel  │                               │
                  │  - payload  │                               │
                  └──────┬──────┘                               │
                         │                                      │
                         ▼                                      │
    ┌────────────────────────────────────────┐                 │
    │              PostgreSQL                 │◀────────────────┤
    │                                         │    Poll every   │
    │  INSERT INTO notifications              │     5 seconds   │
    │  (id, tenant_id, ..., status='pending') │                 │
    └────────────────────┬────────────────────┘                 │
                         │                                      │
                         ▼                                      ▼
                  ┌─────────────┐                        ┌─────────────┐
                  │ Return 201  │                        │   Worker    │
                  │   + UUID    │                        │  picks up   │
                  └─────────────┘                        │  pending    │
                                                         └──────┬──────┘
                                                                │
                                                                ▼
                                                  ┌─────────────────────────┐
                                                  │ Mark as "processing"    │
                                                  └────────────┬────────────┘
                                                               │
                                                               ▼
                                                  ┌─────────────────────────┐
                                                  │     sender.Send()       │
                                                  │       AWS SES           │
                                                  └────────────┬────────────┘
                                                               │
                                          ┌────────────────────┴────────────────────┐
                                          │                                         │
                                          ▼                                         ▼
                                   ┌─────────────┐                           ┌─────────────┐
                                   │   SUCCESS   │                           │   FAILURE   │
                                   │             │                           │             │
                                   │ status =    │                           │ attempt++   │
                                   │   "sent"    │                           │             │
                                   └─────────────┘                           └──────┬──────┘
                                                                                    │
                                                            ┌───────────────────────┴───────────────────────┐
                                                            │                                               │
                                                            ▼                                               ▼
                                                   ┌─────────────────┐                             ┌─────────────────┐
                                                   │ attempt < max   │                             │ attempt >= max  │
                                                   │ (max = 5)       │                             │ (exhausted)     │
                                                   └────────┬────────┘                             └────────┬────────┘
                                                            │                                               │
                                                            ▼                                               ▼
                                                   ┌─────────────────┐                             ┌─────────────────┐
                                                   │ status="pending"│                             │ MoveToDeadLetter│
                                                   │ next_retry_at = │                             │                 │
                                                   │ now + backoff   │                             │ Transaction:    │
                                                   │                 │                             │ 1. Insert DLQ   │
                                                   │ backoff:        │                             │ 2. Update notif │
                                                   │ 2^attempt mins  │                             │    status =     │
                                                   └─────────────────┘                             │  "dead_lettered"│
                                                                                                   └─────────────────┘
```

---

## Future Architecture (Production-Ready)

```
                                         ┌──────────────────┐
                                         │   Route 53       │
                                         │   DNS            │
                                         └────────┬─────────┘
                                                  │
                                                  ▼
                                         ┌──────────────────┐
                                         │   CloudFront     │
                                         │   (CDN + WAF)    │
                                         └────────┬─────────┘
                                                  │
                                                  ▼
                                   ┌──────────────────────────┐
                                   │   Application Load       │
                                   │      Balancer            │
                                   │                          │
                                   │   Health: /health        │
                                   │   SSL Termination        │
                                   └──────────────────────────┘
                                                  │
                 ┌────────────────────────────────┼────────────────────────────────┐
                 │                                │                                │
                 ▼                                ▼                                ▼
┌──────────────────────────┐    ┌──────────────────────────┐    ┌──────────────────────────┐
│    API Pod 1             │    │    API Pod 2             │    │    API Pod N             │
│    (ECS/EKS)             │    │    (ECS/EKS)             │    │    (ECS/EKS)             │
│                          │    │                          │    │                          │
│  ┌────────────────────┐  │    │  ┌────────────────────┐  │    │  ┌────────────────────┐  │
│  │  Rate Limiter      │  │    │  │  Rate Limiter      │  │    │  │  Rate Limiter      │  │
│  │  (Redis-backed)    │  │    │  │  (Redis-backed)    │  │    │  │  (Redis-backed)    │  │
│  │  100 req/min/key   │  │    │  │  100 req/min/key   │  │    │  │  100 req/min/key   │  │
│  └────────────────────┘  │    │  └────────────────────┘  │    │  └────────────────────┘  │
│  ┌────────────────────┐  │    │  ┌────────────────────┐  │    │  ┌────────────────────┐  │
│  │  Auth Middleware   │  │    │  │  Auth Middleware   │  │    │  │  Auth Middleware   │  │
│  │  - API Key header  │  │    │  │  - API Key header  │  │    │  │  - API Key header  │  │
│  │  - JWT Bearer      │  │    │  │  - JWT Bearer      │  │    │  │  - JWT Bearer      │  │
│  └────────────────────┘  │    │  └────────────────────┘  │    │  └────────────────────┘  │
│  ┌────────────────────┐  │    │  ┌────────────────────┐  │    │  ┌────────────────────┐  │
│  │  Metrics           │  │    │  │  Metrics           │  │    │  │  Metrics           │  │
│  │  /metrics          │  │    │  │  /metrics          │  │    │  │  /metrics          │  │
│  │  (Prometheus)      │  │    │  │  (Prometheus)      │  │    │  │  (Prometheus)      │  │
│  └────────────────────┘  │    │  └────────────────────┘  │    │  └────────────────────┘  │
└────────────┬─────────────┘    └────────────┬─────────────┘    └────────────┬─────────────┘
             │                               │                               │
             └───────────────────────────────┼───────────────────────────────┘
                                             │
                      ┌──────────────────────┴──────────────────────┐
                      │                                             │
                      ▼                                             ▼
           ┌─────────────────────┐                       ┌─────────────────────┐
           │     PostgreSQL      │                       │     Amazon SQS      │
           │     (RDS Aurora)    │                       │                     │
           │                     │                       │  ┌───────────────┐  │
           │  Multi-AZ           │                       │  │ notifications │  │
           │  Read Replicas      │                       │  │ (standard)    │  │
           │                     │                       │  └───────────────┘  │
           │  - notifications    │                       │  ┌───────────────┐  │
           │  - dead_letter      │                       │  │ high_priority │  │
           │  - tenants          │                       │  │ (FIFO)        │  │
           │  - api_keys         │                       │  └───────────────┘  │
           │  - templates        │                       │  ┌───────────────┐  │
           └─────────────────────┘                       │  │ dlq           │  │
                                                         │  │ (dead letter) │  │
                                                         │  └───────────────┘  │
                                                         └──────────┬──────────┘
                                                                    │
                 ┌──────────────────────────────────────────────────┼──────────────────────────────────────────────────┐
                 │                                                  │                                                  │
                 ▼                                                  ▼                                                  ▼
┌──────────────────────────┐                      ┌──────────────────────────┐                      ┌──────────────────────────┐
│    Worker Pod 1          │                      │    Worker Pod 2          │                      │    Worker Pod N          │
│                          │                      │                          │                      │                          │
│  ┌────────────────────┐  │                      │  ┌────────────────────┐  │                      │  ┌────────────────────┐  │
│  │  SQS Consumer      │  │                      │  │  SQS Consumer      │  │                      │  │  SQS Consumer      │  │
│  │  Long Polling (20s)│  │                      │  │  Long Polling (20s)│  │                      │  │  Long Polling (20s)│  │
│  └────────────────────┘  │                      │  └────────────────────┘  │                      │  └────────────────────┘  │
│  ┌────────────────────┐  │                      │  ┌────────────────────┐  │                      │  ┌────────────────────┐  │
│  │  Worker Pool       │  │                      │  │  Worker Pool       │  │                      │  │  Worker Pool       │  │
│  │  (10 goroutines)   │  │                      │  │  (10 goroutines)   │  │                      │  │  (10 goroutines)   │  │
│  └────────────────────┘  │                      │  └────────────────────┘  │                      │  └────────────────────┘  │
│  ┌────────────────────┐  │                      │  ┌────────────────────┐  │                      │  ┌────────────────────┐  │
│  │  Channel Router    │  │                      │  │  Channel Router    │  │                      │  │  Channel Router    │  │
│  │  ├─ EmailSender    │  │                      │  │  ├─ EmailSender    │  │                      │  │  ├─ EmailSender    │  │
│  │  ├─ SMSSender      │  │                      │  │  ├─ SMSSender      │  │                      │  │  ├─ SMSSender      │  │
│  │  └─ WebhookSender  │  │                      │  │  └─ WebhookSender  │  │                      │  │  └─ WebhookSender  │  │
│  └────────────────────┘  │                      │  └────────────────────┘  │                      │  └────────────────────┘  │
└────────────┬─────────────┘                      └────────────┬─────────────┘                      └────────────┬─────────────┘
             │                                                 │                                                 │
             └─────────────────────────────────────────────────┼─────────────────────────────────────────────────┘
                                                               │
                        ┌──────────────────────────────────────┼──────────────────────────────────────┐
                        │                                      │                                      │
                        ▼                                      ▼                                      ▼
               ┌─────────────────┐                    ┌─────────────────┐                    ┌─────────────────┐
               │    AWS SES      │                    │    Twilio       │                    │   Webhook       │
               │                 │                    │                 │                    │                 │
               │    Email        │                    │    SMS          │                    │    HTTP POST    │
               │    Delivery     │                    │    Delivery     │                    │    + Retry      │
               └─────────────────┘                    └─────────────────┘                    └─────────────────┘


┌─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                              SUPPORTING SERVICES                                                     │
│                                                                                                                      │
│   ┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐                    │
│   │   ElastiCache   │      │   S3            │      │   Secrets       │      │   Parameter     │                    │
│   │   (Redis)       │      │                 │      │   Manager       │      │   Store         │                    │
│   │                 │      │   - templates/  │      │                 │      │                 │                    │
│   │   - rate limits │      │     email.html  │      │   - DB creds    │      │   - feature     │                    │
│   │   - dedup cache │      │     sms.txt     │      │   - API keys    │      │     flags       │                    │
│   │   - sessions    │      │   - attachments │      │   - Twilio SID  │      │   - config      │                    │
│   └─────────────────┘      └─────────────────┘      └─────────────────┘      └─────────────────┘                    │
│                                                                                                                      │
└─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘


┌─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                              OBSERVABILITY                                                           │
│                                                                                                                      │
│   ┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐                    │
│   │   CloudWatch    │      │   X-Ray         │      │   Prometheus    │      │   Grafana       │                    │
│   │                 │      │                 │      │                 │      │                 │                    │
│   │   - App logs    │      │   - Traces      │      │   - API latency │      │   - Dashboards  │                    │
│   │   - Metrics     │      │   - Service map │      │   - Queue depth │      │   - Alerts      │                    │
│   │   - Alarms      │      │   - Latency     │      │   - Error rate  │      │   - SLOs        │                    │
│   └─────────────────┘      └─────────────────┘      └─────────────────┘      └─────────────────┘                    │
│                                                            │                                                         │
│                                                            ▼                                                         │
│                                                   ┌─────────────────┐                                                │
│                                                   │   PagerDuty     │                                                │
│                                                   │                 │                                                │
│                                                   │   - DLQ alerts  │                                                │
│                                                   │   - Error spike │                                                │
│                                                   │   - SLA breach  │                                                │
│                                                   └─────────────────┘                                                │
│                                                                                                                      │
└─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## Component Comparison: Current vs Future

| Component | Current | Future |
|-----------|---------|--------|
| **Deployment** | Single Docker container | ECS/EKS with auto-scaling |
| **Load Balancing** | None | ALB with health checks |
| **API Instances** | 1 | N (auto-scaled 2-10) |
| **Worker Instances** | 1 goroutine in same process | N pods (auto-scaled 2-20) |
| **Message Queue** | PostgreSQL polling (5s) | Amazon SQS (long polling) |
| **Rate Limiting** | None | Redis token bucket (per tenant) |
| **Authentication** | None | API Key + JWT |
| **Email** | AWS SES | AWS SES |
| **SMS** | Not implemented | Twilio |
| **Webhook** | Not implemented | HTTP client with circuit breaker |
| **Caching** | None | Redis (templates, dedup) |
| **Database** | Single PostgreSQL | RDS Aurora (Multi-AZ, read replicas) |
| **Metrics** | Zap structured logs | Prometheus + Grafana |
| **Tracing** | None | AWS X-Ray / OpenTelemetry |
| **Alerting** | None | PagerDuty / Slack |
| **DLQ Management** | API only | API + Dashboard + Alerts |

---

## Priority Roadmap

### Phase 1: Production Hardening (Current Sprint)
```
[x] Dead Letter Queue with retry/discard
[ ] Prometheus metrics endpoint (/metrics)
[ ] Health check improvements (deep check with DB ping)
[ ] Graceful shutdown with drain
[ ] Request ID propagation
```

### Phase 2: Security & Control (Next Sprint)
```
[ ] API Key authentication middleware
[ ] Per-tenant rate limiting (Redis)
[ ] Request payload validation (JSON Schema)
[ ] Tenant isolation verification
[ ] Audit logging
```

### Phase 3: Scalability (Month 2)
```
[ ] SQS integration (replace DB polling)
[ ] Separate worker binary/deployment
[ ] Connection pooling tuning
[ ] Horizontal pod autoscaling
[ ] Database read replicas
```

### Phase 4: Channel Expansion (Month 3)
```
[ ] SMS via Twilio
[ ] Webhook delivery with circuit breaker
[ ] Template engine (S3 + caching)
[ ] Attachment support (S3 presigned URLs)
[ ] Scheduled notifications
```

### Phase 5: Observability (Month 4)
```
[ ] OpenTelemetry integration
[ ] Grafana dashboards
[ ] SLO tracking (99.9% delivery)
[ ] PagerDuty integration
[ ] Cost tracking per tenant
```

---

## Quick Reference

### Current Endpoints
```
POST   /v1/notifications              Create notification
GET    /v1/notifications              List by tenant
GET    /v1/notifications/{id}         Get single notification
PATCH  /v1/notifications/{id}/status  Update status

GET    /v1/dlq                        List DLQ items
GET    /v1/dlq/{id}                   Get DLQ item
POST   /v1/dlq/{id}/retry             Retry from DLQ
POST   /v1/dlq/{id}/discard           Discard from DLQ

GET    /health                        Health check
```

### Environment Variables
```
PORT=8080
ENV=development
LOG_LEVEL=info
DB_HOST=localhost
DB_PORT=5432
DB_USER=nimbus
DB_PASSWORD=nimbus
DB_NAME=nimbus
DB_SSLMODE=disable
AWS_REGION=us-east-1
SES_FROM_EMAIL=noreply@example.com
```
