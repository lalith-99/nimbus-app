# Postman Collection

Postman collection for testing the Nimbus API.

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

```bash
go run ./cmd/gateway
```

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
- Select "Create Email Notification"
- Click Send
- Copy the returned `id`

### 2. Get the Notification
- Select "Get Notification by ID"
- Replace `{{notification_id}}` with the copied ID
- Click Send

### 3. Update Status
- Select "Update Status - Mark as Sent"
- Make sure `{{notification_id}}` is set
- Click Send

### 4. List All Notifications
- Select "List Notifications (Default)"
- Click Send

## Environment Variables

The collection uses these variables (already configured):

| Variable | Value | Description |
|----------|-------|-------------|
| `base_url` | `http://localhost:8080` | API base URL |
| `tenant_id` | `00000000-0000-0000-0000-000000000001` | Default tenant UUID |
| `user_id` | `00000000-0000-0000-0000-000000000002` | Default user UUID |
| `notification_id` | (empty) | Set this after creating a notification |

## Auto-save Notification ID

Add this test script to "Create Email Notification":

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

# Run collection
newman run Nimbus_API.postman_collection.json \
  --environment Nimbus_Local.postman_environment.json
```

## Troubleshooting

**Request failed:**
- Check if server is running (`go run ./cmd/gateway`)
- Verify environment is selected (top right dropdown)
- Confirm `base_url` is `http://localhost:8080`

**Invalid UUID error:**
- Check `tenant_id` and `user_id` in environment
- Ensure `notification_id` is set after creating a notification

**404 Not Found:**
- Create a notification first
- Verify the `notification_id` is correct

