# Local webhook demo

Run Nimbus with PostgreSQL, Redis, and a deterministic webhook receiver. The
walkthrough uses the database polling worker and local webhook delivery, with no
paid AWS or OpenAI calls. You need Docker Engine/Desktop with Docker Compose v2;
no Go, Python, AWS account, or cloud credentials are required on the host.

## Start and verify

From the repository root:

```sh
make demo
```

Equivalent commands, also used by CI:

```sh
docker compose up --build --wait --wait-timeout 120
docker compose run --rm smoke
```

The first build downloads images and Go dependencies. The smoke command exits
nonzero on any failed check, uses 3-second HTTP timeouts and 25-second transition
deadlines, and has a 120-second total deadline. A healthy run normally spends most
of its time waiting for the worker's 5-second polling interval.

The script checks:

1. Six concurrent REST creates share one tenant and idempotency key. The tenant has one stored notification, which becomes `sent`.
2. Repeating its `Idempotency-Key` returns the same notification ID and the
   `X-Idempotency-Replayed: true` header. The local receiver observes one delivery.
3. An actual webhook `503` leaves the notification `pending` with a scheduled
   retry. A later actual delivery succeeds.
4. A worker failure at the retry limit creates a DLQ record.
5. Six concurrent retries of one DLQ record produce one replacement. The worker delivers it and the original DLQ record points to that replacement.

**Retry/DLQ fixture boundary:** normal retry delays are 1 minute, 5 minutes, then
15 minutes. To keep this walkthrough short, the script uses the existing status
API to clear `next_retry_at` after observing a real failed attempt. For the DLQ
case it also seeds `attempt=4` before the next real worker failure. That case makes
**two actual webhook attempts**, not five. The script prints each state adjustment.
It verifies real worker/API transitions after setup, not elapsed backoff timing,
load capacity, or exactly-once external delivery. The normal retry schedule is unchanged.

## Services and boundaries

| Service | Access | Purpose |
|---|---|---|
| REST API | `http://127.0.0.1:8080` | Create and inspect notifications, DLQ operations |
| gRPC API | `127.0.0.1:9090` | Authenticated unary and streaming RPCs |
| Webhook fixture | `http://127.0.0.1:9080` | Inspect deliveries and configure local failures |
| PostgreSQL/Redis | Compose network only | Durable notification state and request deduplication |

HTTP, gRPC, and fixture ports are bound to loopback; PostgreSQL and Redis do not
publish host ports. The stack uses a standard Docker bridge so the documented
localhost endpoints are reachable. **This network allows outbound connectivity; it
is not an egress isolation boundary.**

The walkthrough avoids paid provider calls by submitting only the local `webhook`
channel. AWS SDK clients receive fixed dummy credentials; SQS queue URLs and the
OpenAI key are explicitly empty, even if the host has a populated `.env`. Email,
SMS, SQS, and AI/RAG integrations are not exercised. Changing those settings or
submitting requests to external delivery providers is outside this local demo.

This is a local development configuration. The REST routes include administrative
operations and do not establish a production authentication/authorization boundary.
Do not publish this stack to the internet.

To avoid occupied host ports, set `NIMBUS_HTTP_PORT`, `NIMBUS_GRPC_PORT`, or
`NIMBUS_WEBHOOK_PORT` before starting Compose. Container-to-container ports and the
smoke command do not change.

## Send one notification manually

The fixture accepts unknown scenario names with a `200` response by default:

```sh
curl --fail-with-body -sS http://127.0.0.1:8080/v1/notifications \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: local-manual-demo-1' \
  -H 'X-Tenant-ID: 00000000-0000-0000-0000-000000000001' \
  -d '{
    "tenant_id": "00000000-0000-0000-0000-000000000001",
    "user_id": "00000000-0000-0000-0000-000000000002",
    "channel": "webhook",
    "payload": {
      "url": "http://webhook:9080/webhooks/manual",
      "body": {"message": "Hello from Nimbus"}
    }
  }'
```

Copy the returned `id`, then inspect it after a worker poll:

```sh
curl --fail-with-body -sS http://127.0.0.1:8080/v1/notifications/NOTIFICATION_ID
curl --fail-with-body -sS http://127.0.0.1:9080/scenarios/manual
```

Repeating the creation request with the same key replays its cached response;
choose a new key for a new notification. Explicit-key cache entries currently live
for 24 hours. This request-level deduplication does not guarantee exactly-once
delivery at an external provider.

To make the next manual delivery fail once:

```sh
curl --fail-with-body -sS -X PUT http://127.0.0.1:9080/scenarios/manual \
  -H 'Content-Type: application/json' -d '{"failures":1}'
```

Create another notification with a new idempotency key. `failures:0` restores
success and `failures:-1` keeps returning `503`. Changing a scenario preserves its
delivery history. Fixture history is in memory and resets when its container restarts.

## Try gRPC with an existing notification

If `grpcurl` is installed, the manual notification above belongs to the tenant
associated with the development token. Use the checked-in protobuf because server
reflection is not enabled:

```sh
grpcurl -plaintext \
  -import-path proto -proto notification/v1/notification.proto \
  -H 'authorization: Bearer dev-token-nimbus' \
  -d '{"id":"NOTIFICATION_ID"}' \
  127.0.0.1:9090 notification.v1.NotificationService/GetNotification
```

For status streaming, use the same options with:

```text
-d '{"notification_id":"NOTIFICATION_ID"}'
notification.v1.NotificationService/StreamDeliveryUpdates
```

The default development token is public fixture data, not a production credential.
The smoke script covers the REST delivery flow; the commands above are an optional
gRPC walkthrough, not a claim that Compose smoke validates gRPC.

## Database initialization and repeat runs

Compose uses a PostgreSQL image with pgvector so migration `003` can enable the
`vector` extension. A separate `migrate` service runs the repository's migrator,
which applies only `*.up.sql` files and records their names in `schema_migrations`.
The gateway starts only after migrations finish successfully. Mounting the whole
migrations directory under PostgreSQL's init directory would also execute rollback
files, so this stack does not do that.

The `nimbus-local` Compose project uses dedicated `postgres_demo_data` and
`redis_demo_data` volumes. Re-running the smoke command uses a fresh tenant and keys;
it does not delete existing records. Previously initialized volumes from the old
Compose configuration are not migrated by this demo.

Useful diagnostics:

```sh
docker compose ps -a
docker compose logs migrate gateway webhook
docker compose exec postgres psql -U nimbus -d nimbus -c 'TABLE schema_migrations;'
docker compose exec postgres psql -U nimbus -d nimbus -c '\dx'
```

Stop the stack while keeping its data:

```sh
make demo-down
```

For an intentional clean reset of **only this demo's persisted data**, run
`docker compose down --volumes`, then `make demo`. The reset deletes the demo's
PostgreSQL and Redis volumes; it is not part of the normal smoke command.
