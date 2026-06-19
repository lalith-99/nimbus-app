# Nimbus — API Reference

Complete reference for the Nimbus notification platform. Nimbus exposes **two transports**:

| Transport | Base URL | Audience | Encoding |
|---|---|---|---|
| **REST** | `http://localhost:8080` | External clients, browsers, 3rd parties | JSON |
| **gRPC** | `localhost:9090` | Internal microservices | Protobuf (binary) |

> For the *why* behind two transports and the system design, see
> [ARCHITECTURE.md](ARCHITECTURE.md).

---

## Table of Contents

- [Conventions](#conventions)
  - [Identifiers](#identifiers)
  - [Error Format](#error-format-problemjson)
  - [Idempotency](#idempotency)
  - [Rate Limiting](#rate-limiting)
  - [Enumerations](#enumerations)
- [REST API](#rest-api)
  - [Health & Ops](#health--ops)
  - [Notifications](#notifications)
  - [Dead Letter Queue](#dead-letter-queue)
  - [AI Endpoints](#ai-endpoints)
- [gRPC API](#grpc-api)
- [Status Codes Summary](#status-codes-summary)

---

## Conventions

### Identifiers

All `id`, `tenant_id`, and `user_id` values are **UUID v4** strings, e.g.
`00000000-0000-0000-0000-000000000001`. Invalid UUIDs return `400`.

### Error Format (problem+json)

Every error response uses `Content-Type: application/problem+json`
([RFC 7807](https://datatracker.ietf.org/doc/html/rfc7807)) with this shape:

```json
{
  "type": "invalid_request",
  "title": "Invalid channel",
  "status": 400,
  "detail": "channel must be email, sms, or webhook"
}
```

| Field | Description |
|---|---|
| `type` | Stable machine-readable error category (see [catalog](#error-catalog)). |
| `title` | Short human-readable summary. |
| `status` | Mirrors the HTTP status code. |
| `detail` | Optional, more specific explanation. |

### Idempotency

`POST /v1/notifications` is **idempotent** when Redis is enabled.

| Header | Direction | Meaning |
|---|---|---|
| `Idempotency-Key: <string>` | request | Client-controlled dedup key. Retained **24 h**. Recommended for all writes. |
| `X-Idempotency-Replayed: true` | response | The response was served from cache, not freshly created. |

- **No header supplied?** Nimbus auto-generates a content-hash key
  (`hash(tenant|user|channel|payload)`) retained **5 minutes** — this absorbs accidental network
  retries without blocking intentional re-sends.
- A request that arrives **while an identical one is still in flight** returns `409 Conflict`
  (`duplicate_request`).

```bash
curl -X POST http://localhost:8080/v1/notifications \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: order-4711-confirmation" \
  -d '{ "tenant_id": "...", "user_id": "...", "channel": "email", "payload": {"to":"a@b.com"} }'
```

### Rate Limiting

All `/v1/*` routes are rate limited **per tenant** using a Redis sliding window.

| Limit | Window | Scope |
|---|---|---|
| 100 requests | 60 seconds (rolling) | per `tenant_id` |

Exceeding the limit returns `429 Too Many Requests`. (If Redis is unavailable, rate limiting is
disabled and requests pass through — fail-open.)

### Enumerations

| Enum | Values |
|---|---|
| `channel` | `email` · `sms` · `webhook` |
| notification `status` | `pending` · `processing` · `sent` · `failed` · `dead_lettered` |
| DLQ `status` | `pending` · `retried` · `discarded` |

---

## REST API

### Health & Ops

#### `GET /health`
Liveness probe. Returns `200 OK` with body `OK`.

#### `GET /metrics`
Prometheus exposition format. Key series:

| Metric | Type | Labels |
|---|---|---|
| `nimbus_http_requests_total` | counter | `method`, `path`, `status` |
| `nimbus_http_request_duration_seconds` | histogram | `method`, `path` |
| `nimbus_notifications_enqueued_total` | counter | `tenant_id`, `channel` |
| `nimbus_notifications_processed_total` | counter | `status`, `channel` |
| `nimbus_notification_latency_seconds` | histogram | `channel` |
| `nimbus_sqs_messages_in_flight` | gauge | — |
| `nimbus_idempotency_hits_total` | counter | — |
| `nimbus_rate_limit_rejections_total` | counter | `tenant_id` |

#### `GET /v1/health/circuits`
Live state of every downstream circuit breaker.

```json
{
  "circuit_breakers": [
    { "name": "ses-email", "state": "closed", "failures": 0 },
    { "name": "sns-sms",   "state": "open",   "failures": 5 },
    { "name": "webhook",   "state": "closed", "failures": 0 }
  ]
}
```

#### `POST /v1/admin/circuits/{name}/reset`
Force a breaker back to `closed` (next request becomes a probe). `name` ∈
`ses-email`, `sns-sms`, `webhook`. Returns `200` with `{"status":"reset","breaker":"..."}` or
`404` if the name is unknown.

---

### Notifications

#### `POST /v1/notifications`
Create (enqueue) a notification.

**Request body**

| Field | Type | Required | Notes |
|---|---|---|---|
| `tenant_id` | UUID | ✓ | Owning tenant. |
| `user_id` | UUID | ✓ | Triggering user. |
| `channel` | enum | ✓ | `email` \| `sms` \| `webhook`. |
| `payload` | JSON object | ✓ | Channel-specific (see below). |

**Channel payloads**

```jsonc
// email
{ "to": "user@example.com", "subject": "Welcome", "body": "Hello!" }

// sms
{ "to": "+15551234567", "body": "Your code is 123456" }

// webhook
{ "url": "https://hooks.example.com/x", "body": { "event": "order.shipped" } }
```

**Example**

```bash
curl -X POST http://localhost:8080/v1/notifications \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": "00000000-0000-0000-0000-000000000001",
    "user_id":   "00000000-0000-0000-0000-000000000002",
    "channel":   "email",
    "payload":   { "to": "user@example.com", "subject": "Hi", "body": "Hello" }
  }'
```

**`201 Created`**

```json
{ "id": "7c9e6679-7425-40de-944b-e07fc1f90ae7" }
```

**Errors:** `400` (`invalid_request` — missing fields, bad UUID, bad channel, malformed/invalid
JSON), `409` (`duplicate_request`), `429` (rate limited), `500` (`database_error`).

---

#### `GET /v1/notifications`
List a tenant's notifications (newest first).

| Query param | Type | Default | Notes |
|---|---|---|---|
| `tenant_id` | UUID | — | **Required.** |
| `limit` | int | 20 | 1–100. |
| `offset` | int | 0 | ≥ 0. |

```bash
curl "http://localhost:8080/v1/notifications?tenant_id=00000000-0000-0000-0000-000000000001&limit=20"
```

**`200 OK`**

```json
{
  "data": [
    {
      "id": "7c9e6679-...",
      "tenant_id": "00000000-...-0001",
      "user_id": "00000000-...-0002",
      "channel": "email",
      "payload": { "to": "user@example.com" },
      "status": "sent",
      "attempt": 1,
      "created_at": "2026-06-18T10:00:00Z",
      "updated_at": "2026-06-18T10:00:03Z"
    }
  ],
  "limit": 20,
  "offset": 0,
  "count": 1
}
```

---

#### `GET /v1/notifications/{id}`
Fetch a single notification by UUID. Returns the full record (`200`) or `404` (`not_found`).

---

#### `PATCH /v1/notifications/{id}/status`
Manually transition a notification's status (admin/testing).

**Request body**

| Field | Type | Required | Notes |
|---|---|---|---|
| `status` | enum | ✓ | `pending` \| `processing` \| `sent` \| `failed`. |
| `attempt` | int | ✓ | Must be ≥ 0. |
| `error` | string | — | Optional error message. |

**`200 OK`** → `{ "id": "...", "status": "sent" }`. Errors: `400`, `500`.

---

### Dead Letter Queue

Notifications that exhaust all retries (5 attempts) land here for inspection and recovery.

#### `GET /v1/dlq`
List DLQ items for a tenant. Same pagination params as the notifications list
(`tenant_id` required, `limit`, `offset`).

```json
{
  "data": [
    {
      "id": "a1b2c3d4-...",
      "original_notification_id": "7c9e6679-...",
      "tenant_id": "00000000-...-0001",
      "channel": "email",
      "payload": { "to": "bad@addr" },
      "attempts": 5,
      "last_error": "550 mailbox unavailable",
      "status": "pending",
      "created_at": "2026-06-18T10:05:00Z"
    }
  ],
  "limit": 20, "offset": 0, "count": 1
}
```

#### `GET /v1/dlq/{id}`
Fetch a single DLQ item (`200`) or `404`.

#### `POST /v1/dlq/{id}/retry`
Re-queue a failed item. Creates a **new** notification (`status=pending`) and marks the DLQ item
`retried`.

**`200 OK`** → `{ "id": "...", "status": "retried", "new_notification_id": "..." }`

#### `POST /v1/dlq/{id}/discard`
Permanently abandon a DLQ item (marks it `discarded`).

**`200 OK`** → `{ "id": "...", "status": "discarded" }`

---

### AI Endpoints

> Available only when the server is started with `OPENAI_API_KEY` set (`AI_ENABLED`).

#### `POST /v1/ai/compose`
Turn a natural-language instruction into one or more notifications via LLM **function calling**.

**Request**

```json
{
  "prompt": "Send a welcome email to alice@example.com",
  "tenant_id": "00000000-0000-0000-0000-000000000001",
  "user_id":   "00000000-0000-0000-0000-000000000002"
}
```

**`200 OK`**

```json
{
  "message": "I've sent a welcome email to alice@example.com.",
  "notification_ids": ["7c9e6679-7425-40de-944b-e07fc1f90ae7"]
}
```

Errors: `400` (missing `prompt`/`tenant_id`/`user_id`), `500` (`ai_error`).

#### `POST /v1/ai/ask`
Ask a question answered by the **RAG pipeline** — grounded in the tenant's own knowledge base, with
inline citations. (Pipeline: injection guard → PII mask → embed → hybrid search → rerank → LLM →
PII restore. See [ARCHITECTURE §11](ARCHITECTURE.md#11-the-ai--rag-subsystem).)

**Request**

```json
{ "query": "Did my order confirmation email to alice get delivered?",
  "tenant_id": "00000000-0000-0000-0000-000000000001" }
```

**`200 OK`**

```json
{
  "answer": "Yes — the order confirmation to alice@example.com was delivered on the first attempt [1].",
  "citations": [
    {
      "id": "kb-uuid-1",
      "content": "Email notification to alice@example.com: order confirmation, status sent.",
      "source_type": "notification",
      "source_id": "7c9e6679-..."
    }
  ]
}
```

If nothing relevant is found, `answer` explains that the knowledge base lacks the information and
`citations` is `[]`. Errors: `400` (blocked by injection guard / invalid `tenant_id`), `500`.

---

## gRPC API

**Service:** `notification.v1.NotificationService` · **Port:** `:9090` ·
**Contract:** [proto/notification/v1/notification.proto](../proto/notification/v1/notification.proto)

### Authentication

Every RPC requires a Bearer token in gRPC metadata. The interceptor maps the token → `tenant_id` and
injects it into the request context. **The tenant is always derived from the token, never trusted
from the request body** (IDOR defense).

```
metadata: authorization: Bearer <token>
```

Configure tokens via `GRPC_AUTH_TOKENS="token1:tenant-uuid-1,token2:tenant-uuid-2"`.
Dev default: `dev-token-nimbus` → `00000000-0000-0000-0000-000000000001`.

### RPCs

| RPC | Type | Description |
|---|---|---|
| `CreateNotification` | unary | Enqueue a notification (mirrors `POST /v1/notifications`). |
| `GetNotification` | unary | Fetch one notification (tenant-scoped). |
| `StreamDeliveryUpdates` | **server-streaming** | Push live status updates every 2s until terminal. |

#### `CreateNotification`

```protobuf
rpc CreateNotification(CreateNotificationRequest) returns (CreateNotificationResponse);

message CreateNotificationRequest {
  string tenant_id = 1;  // must match authenticated tenant or be empty
  string user_id   = 2;
  string channel   = 3;  // email | sms | webhook
  bytes  payload   = 4;  // JSON bytes
}
message CreateNotificationResponse {
  string id = 1;
  string status = 2;                          // "pending"
  google.protobuf.Timestamp created_at = 3;
}
```

**gRPC status codes:** `UNAUTHENTICATED` (no tenant in context), `PERMISSION_DENIED`
(body `tenant_id` ≠ token tenant), `INVALID_ARGUMENT` (bad `user_id`/`channel`), `INTERNAL`.

#### `GetNotification`

```protobuf
rpc GetNotification(GetNotificationRequest) returns (Notification);
```

Returns `NOT_FOUND` both when the ID doesn't exist **and** when it belongs to another tenant — so
the API never reveals which IDs exist (no enumeration oracle).

#### `StreamDeliveryUpdates`

```protobuf
rpc StreamDeliveryUpdates(StreamDeliveryUpdatesRequest) returns (stream DeliveryUpdate);

message DeliveryUpdate {
  string notification_id = 1;
  string status          = 2;
  int32  attempt         = 3;
  string error_message   = 4;                 // set when status = "failed"
  google.protobuf.Timestamp updated_at = 5;
}
```

Ownership is verified **once up front**; the stream closes automatically when the notification
reaches a terminal state (`sent` / `failed` / `dead_lettered`).

**Example (grpcurl):**

```bash
grpcurl -plaintext \
  -H "authorization: Bearer dev-token-nimbus" \
  -d '{"notification_id":"7c9e6679-7425-40de-944b-e07fc1f90ae7"}' \
  localhost:9090 notification.v1.NotificationService/StreamDeliveryUpdates
```

---

## Status Codes Summary

### REST

| Code | When |
|---|---|
| `200 OK` | Successful read / update / DLQ action. |
| `201 Created` | Notification created (or idempotent replay). |
| `400 Bad Request` | Validation failure (`invalid_request`). |
| `404 Not Found` | Unknown notification / DLQ item. |
| `409 Conflict` | Idempotency key in flight (`duplicate_request`). |
| `429 Too Many Requests` | Tenant rate limit exceeded. |
| `500 Internal Server Error` | `database_error`, `ai_error`, `internal_error`. |

### gRPC

| Code | When |
|---|---|
| `OK` | Success. |
| `INVALID_ARGUMENT` | Malformed `user_id`, `channel`, or `id`. |
| `UNAUTHENTICATED` | Missing/invalid Bearer token. |
| `PERMISSION_DENIED` | Body `tenant_id` ≠ authenticated tenant. |
| `NOT_FOUND` | Notification missing or owned by another tenant. |
| `INTERNAL` | Server-side failure. |

### Error Catalog

| `type` | HTTP | Meaning |
|---|---|---|
| `invalid_request` | 400 | Validation failed. |
| `duplicate_request` | 409 | Idempotency key already in flight. |
| `not_found` | 404 | Resource does not exist. |
| `database_error` | 500 | Persistence failure. |
| `ai_error` | 500 | AI/LLM processing failure. |
| `internal_error` | 500 | Unclassified server error. |

---

*See [README](../README.md) for setup and [ARCHITECTURE.md](ARCHITECTURE.md) for system design.*
