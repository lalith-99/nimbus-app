# Nimbus

**A Go notification service with REST and gRPC APIs, asynchronous workers, and recoverable delivery workflows.**

[![CI](https://github.com/lalith-99/nimbus-app/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/lalith-99/nimbus-app/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.23-00ADD8?logo=go&logoColor=white)](go.mod)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-pgvector-4169E1?logo=postgresql&logoColor=white)](migrations/)
[![Redis](https://img.shields.io/badge/Redis-request%20deduplication-DC382D?logo=redis&logoColor=white)](internal/redis/)

[Run the local demo](#quick-start) · [Architecture](docs/ARCHITECTURE.md) · [API reference](docs/API.md) · [Verification](#verification)

Nimbus is a personal backend engineering project focused on the work that happens **after an API accepts a request**: coordinating workers, handling provider failures, scheduling retries, and making failed work inspectable and recoverable.

The service stores notification requests in PostgreSQL and dispatches them to email through AWS SES, SMS through AWS SNS, or HTTP webhooks. Redis supports REST request deduplication and HTTP rate limiting. An optional SQS consumer shares database claim logic with the polling worker. The local demo uses only a webhook receiver, PostgreSQL, and Redis; it needs no cloud account.

## Quick start

Requirements: Git and Docker with the Docker Compose plugin.

```bash
git clone https://github.com/lalith-99/nimbus-app.git
cd nimbus-app
docker compose up --build --wait
docker compose run --rm smoke
```

The stack starts PostgreSQL with pgvector, applies forward migrations through a versioned migration job, starts Redis and a local webhook receiver, and exposes:

| Interface | Local address |
|---|---|
| REST API | `http://localhost:8080` |
| gRPC API | `localhost:9090` |
| Prometheus metrics | `http://localhost:8080/metrics` |

The smoke walkthrough checks notification creation and delivery status, replay of an `Idempotency-Key`, a failed webhook attempt followed by retry, and dead-letter recovery. Retry timing is advanced using **explicit local database fixtures**; the walkthrough does not claim to wait through the normal retry schedule or simulate five real provider failures.

Compose uses loopback-only published ports and fixed dummy AWS credentials. PostgreSQL and Redis stay on the Compose network without published ports. The walkthrough sends only local webhooks; email, SMS, SQS, and AI calls are not part of it. The standard bridge network permits outbound connections.

See **[the step-by-step local walkthrough](docs/LOCAL_DEMO.md)** for manual requests, expected behavior, gRPC examples, and how the retry fixtures work. If you used an older Compose setup, read its existing-volume notes before starting.

```bash
# Stop containers; keep the local database volume.
docker compose down
```

## Design decisions

| Decision | Why it is implemented this way | Tradeoff |
|---|---|---|
| **PostgreSQL stores the durable work record.** | The API inserts a pending notification before acknowledging creation. Workers can discover it without relying on an SQS publish succeeding. | Database availability and write throughput remain dependencies. This is a database-backed work queue, not an atomic transaction spanning PostgreSQL and SQS. |
| **SQS and polling share database claims.** | REST can publish a best-effort SQS notification after the insert. The consumer claims one eligible row; the poller claims a batch with `FOR UPDATE SKIP LOCKED`. | Claims coordinate current ownership, but do not make an external provider send exactly once. Delayed retries and stale-work recovery still depend on polling. |
| **Retry and dead-letter state stays inspectable.** | The worker records attempts and next retry times; exhausted work moves to a database dead-letter table with inspect, retry, and discard endpoints. | Recovery needs an operating database and workers. A manual retry creates a new notification and may repeat an external side effect. |

The polling interval is 5 seconds without SQS and 30 seconds when SQS is configured. SQS publishing happens synchronously in the REST handler after the database insert and is best effort. The gRPC create path currently writes directly to the database and relies on the polling worker.

### Delivery semantics and limits

- A successful creation response means the notification was persisted, not delivered to its recipient.
- Redis request deduplication applies to REST creation while the key and cached result remain available. Requests continue if Redis is unavailable. PostgreSQL insertion and Redis result caching are separate writes, so a failure or reservation expiry between them can allow a duplicate row. An expired reservation cannot release a newer request's lock or replace its cached response.
- After a claim expires and another worker reclaims it, PostgreSQL rejects status and dead-letter writes from the old worker. [The PostgreSQL regression test](internal/db/claim_test.go) exercises delayed success, retry, and dead-letter writes before and after completion.
- A worker can send successfully and crash before recording `sent`. Reclaiming or retrying that row can send again. Long-running work can also outlive the stale-claim threshold. **Exactly-once external delivery is not guaranteed.**
- Retries are bounded: the worker uses delays of 1, 5, and then 15 minutes, with a maximum of 5 attempts before dead-letter handling. Provider acceptance is not proof that a person received or read a message.
- The project includes tenant IDs and gRPC token-to-tenant checks. The REST routes do not currently enforce tenant authentication, and the configuration includes a development gRPC token. The supplied Compose setup is for local development, not a hardened public deployment.

The [architecture guide](docs/ARCHITECTURE.md) explains state transitions, failure windows, and the assumptions behind the design.

## Implemented capabilities

| Area | Implementation |
|---|---|
| APIs | REST/JSON with Chi; gRPC/Protocol Buffers for create, get, and streamed status updates. |
| Asynchronous processing | PostgreSQL pending records, batch/single-row claims, shared dispatch logic, and stale-processing recovery. |
| Delivery | AWS SES email, AWS SNS SMS, and HTTP POST webhooks. |
| Recovery | Retry scheduling and database dead-letter inspect/retry/discard endpoints. |
| Request deduplication | Tenant-scoped Redis keys; automatic content-hash keys with a 5-minute TTL and client keys with a 24-hour TTL. |
| Rate limiting | Redis sorted-set sliding windows for HTTP requests; concurrent checks and updates are not a strict atomic quota. |
| Circuit breakers | Per-channel closed/open/half-open state and bounded probes. |
| Status streaming | One gRPC stream; the server checks PostgreSQL every 2 seconds. This reduces repeated client calls, but retains server-side database polling. |
| Observability | Prometheus metrics, structured zap logs, health and circuit-status endpoints. |
| Packaging | Docker, Compose, forward SQL migrations, and Terraform infrastructure definitions. |

An optional AI extension provides notification composition and retrieval-augmented Q&A using OpenAI and pgvector. It is disabled in the local demo and is documented separately in [AI Integration](ai-integration/README.md).

## Verification

The [GitHub Actions workflow](https://github.com/lalith-99/nimbus-app/actions/workflows/ci.yml) runs the Go test suite with the race detector, builds the gateway, and runs the local Compose smoke walkthrough. Open the linked run to inspect its commit, job results, and demo output.

```bash
go test -race ./...
go build ./cmd/gateway
docker compose up --build --wait
docker compose run --rm smoke
```

The [local smoke script](scripts/local-demo/smoke.py) races six REST creates using one key and verifies one row and one webhook. It also races six dead-letter retries and checks the recovered notification. [Redis tests](internal/redis/idempotency_test.go) exercise reservation expiry and delayed writers.

The tests exercise REST validation and repository interactions, gRPC tenant checks, worker success/retry/dead-letter paths, SQS consumer acknowledgement decisions, Redis request reservation/replay, rate limiting, and circuit-breaker transitions. Many unit tests use fakes; the Compose walkthrough exercises the real gateway, PostgreSQL, Redis, and local webhook receiver together.

The benchmarks in [`internal/sqs/producer_bench_test.go`](internal/sqs/producer_bench_test.go) measure local message construction and JSON serialization. They do **not** establish live SQS enqueue latency, provider delivery latency, or end-to-end throughput. No production capacity or latency result is claimed here.

AWS image publishing, migration, and deployment jobs are currently disabled in CI. Terraform files demonstrate infrastructure configuration; they are not evidence of a currently running service.

## Configuration and API

Configuration is read from environment variables in [`internal/config/config.go`](internal/config/config.go). [`docker-compose.yaml`](docker-compose.yaml) supplies the local demo values.

| Variables | Purpose |
|---|---|
| `PORT`, `GRPC_PORT` | HTTP and gRPC listeners; default 8080 and 9090. |
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSLMODE` | PostgreSQL connection. |
| `REDIS_HOST`, `REDIS_PORT`, `REDIS_PASSWORD`, `REDIS_DB` | Redis request deduplication and HTTP rate limiting. |
| `AWS_REGION`, `SES_FROM_EMAIL`, `SNS_REGION` | Optional email and SMS delivery integrations. |
| `SQS_QUEUE_URL`, `SQS_DLQ_URL`, `SQS_REGION` | Optional SQS path; the database polling worker continues to run. |
| `GRPC_AUTH_TOKENS` | Comma-separated `token:tenantUUID` mappings; current configuration also retains the development token. |
| `OPENAI_API_KEY`, `OPENAI_MODEL` | Optional AI extension; no key is needed for the notification demo. |

| Method | Endpoint | Purpose |
|---|---|---|
| `POST` | `/v1/notifications` | Persist a notification; REST supports request deduplication. |
| `GET` | `/v1/notifications`, `/v1/notifications/{id}` | List by tenant or inspect one notification. |
| `PATCH` | `/v1/notifications/{id}/status` | Update status. |
| `GET` | `/v1/dlq`, `/v1/dlq/{id}` | Inspect database dead-letter records. |
| `POST` | `/v1/dlq/{id}/retry`, `/v1/dlq/{id}/discard` | Retry or discard dead-lettered work. |
| `GET` | `/health`, `/v1/health/circuits`, `/metrics` | Health, breaker state, and Prometheus metrics. |

The `notification.v1.NotificationService` gRPC service exposes `CreateNotification`, `GetNotification`, and `StreamDeliveryUpdates`. See the [API reference](docs/API.md) and [local demo](docs/LOCAL_DEMO.md).

## Code and documentation

| Start here | What to look for |
|---|---|
| [`cmd/gateway`](cmd/gateway/) | Dependency wiring, API routes, worker configuration, and optional integrations. |
| [`internal/db`](internal/db/) | Durable work records, claims, stale recovery, and dead-letter transactions. |
| [`internal/worker`](internal/worker/) | Shared dispatch logic, provider adapters, and SQS consumer behavior. |
| [`internal/redis`](internal/redis/) | Request reservations, cached responses, and rate limiting. |
| [Architecture](docs/ARCHITECTURE.md) · [Diagrams](ARCHITECTURE_DIAGRAMS.md) | Design decisions, state transitions, and failure handling. |
| [Engineering internals](docs/INTERNALS.md) · [API reference](docs/API.md) | Implementation walkthrough and contracts. |
| [Local demo](docs/LOCAL_DEMO.md) · [Postman guide](postman/README.md) | Reproduce and inspect notification workflows. |
| [`terraform`](terraform/) · [Migration guide](docs/migrations.md) | AWS infrastructure definitions and schema changes. |
