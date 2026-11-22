# Postman Collection

Postman collection for exploring the Nimbus REST API. Start with the local webhook flow to
observe actual delivery without an AWS account.

## Setup

### Import Collection

1. Open Postman
2. Click Import
3. Select `Nimbus_API.postman_collection.json`

### Import Environment

1. Click Environments (left sidebar)
2. Click Import
3. Select `Nimbus_Local.postman_environment.json`

### Activate Environment

1. Select environment dropdown (top right)
2. Choose "Nimbus Local"

### Start Server

From the repository root:

```bash
docker compose up --build --wait --wait-timeout 120
docker compose run --rm smoke
```

Compose applies the schema and starts PostgreSQL, Redis, the gateway, and a local webhook
receiver. The smoke script is the repeatable pass/fail check; the collection includes manual
status changes and optional provider examples, so running every request is not an equivalent
delivery test. See [Local demo](../docs/LOCAL_DEMO.md) for the expected output.

## Collection Structure

```
Nimbus Notification API/
├── Health/
│   └── Health Check
├── Notifications/
│   ├── Create Email Notification
│   ├── Create SMS Notification
│   ├── Create Webhook Notification
│   ├── Get Notification by ID
│   ├── List Notifications (Default)
│   ├── List Notifications (Paginated)
│   ├── Update Status - Mark as Sent
│   ├── Update Status - Mark as Failed
│   └── Update Status - Mark as Processing
└── Error Cases/
    ├── Invalid Tenant ID
    ├── Invalid Channel
    ├── Missing Required Fields
    ├── Invalid Status Update
    └── Non-Existent Notification
```

## Usage

### 1. Create a Notification

- Select "Create Webhook Notification"
- Keep the payload URL as `http://webhook:9080/webhooks/postman`. The gateway resolves this
  hostname inside Compose; Postman still calls the gateway at `http://localhost:8080`.
- Click Send
- Copy the returned `id` into the environment's `notification_id` value. `201` means the request
  was persisted; the polling worker sends it later.

### 2. Get the Notification

- Select "Get Notification by ID"
- Check that the environment's `notification_id` contains the copied ID
- Click Send
- Check again after a worker poll; the status should become `sent` for the local fixture.
- Open `http://localhost:9080/scenarios/postman` to inspect the receiver's delivery history.

### 3. Update Status

This is an administrative/testing operation. It changes the database record; marking a row
`sent` does not deliver a webhook or prove that any provider accepted it. Inspect worker-driven
delivery before trying these requests.

- Select "Update Status - Mark as Sent"
- Make sure `{{notification_id}}` is set
- Click Send

### 4. List All Notifications

- Select "List Notifications (Default)"
- Click Send

### Optional Email and SMS Examples

The default Compose demo uses dummy AWS credentials. Email and SMS creation can still return
`201`, but successful provider delivery requires separately configured AWS access and service
settings. The SMS payload uses `phone_number` and `message`; email uses `to`, `subject`, and `body`.

When repeating a create request, remember that identical content is deduplicated for 5 minutes
while Redis state is available. Add an `Idempotency-Key` to control replay explicitly, using a new
key for a new operation. Add `X-Tenant-ID: {{tenant_id}}` to exercise the HTTP rate limiter; the JSON
body's tenant ID does not supply that middleware key. REST currently has no tenant authorization.

## Environment Variables

The collection uses these variables (already configured):

| Variable | Value | Description |
|----------|-------|-------------|
| `base_url` | `http://localhost:8080` | API base URL |
| `tenant_id` | `00000000-0000-0000-0000-000000000001` | Default tenant UUID |
| `user_id` | `00000000-0000-0000-0000-000000000002` | Default user UUID |
| `notification_id` | (empty) | Set this after creating a notification |

## Auto-save Notification ID

Optionally add this test script to "Create Webhook Notification":

```javascript
if (pm.response.code === 201) {
    var response = pm.response.json();
    pm.environment.set("notification_id", response.id);
}
```

## Run Collection from CLI

```bash
# Install Newman
npm install -g newman

# Run only the Health folder from the repository root
newman run postman/Nimbus_API.postman_collection.json \
  --environment postman/Nimbus_Local.postman_environment.json \
  --folder Health
```

Use `docker compose run --rm smoke` for the verified create/replay/retry/DLQ flow. The full
collection contains cloud provider examples, intentional errors, and status mutations; it is
not configured as an automated delivery acceptance suite.

## Troubleshooting

**Request failed:**
- Check service health with `docker compose ps -a` and inspect `docker compose logs gateway migrate`
- Verify environment is selected (top right dropdown)
- Confirm `base_url` is `http://localhost:8080`

**Invalid UUID error:**
- Check `tenant_id` and `user_id` in environment
- Ensure `notification_id` is set after creating a notification

**404 Not Found:**
- Create a notification first
- Verify the `notification_id` is correct

