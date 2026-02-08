#!/usr/bin/env bash
set -euo pipefail

# Nimbus real-world channel test script
# Fill these values before running.
BASE_URL="${BASE_URL:-http://localhost:8080}"
TENANT_ID="${TENANT_ID:-00000000-0000-0000-0000-000000000001}"
USER_ID="${USER_ID:-00000000-0000-0000-0000-000000000002}"

# Add timestamp for unique payloads each run
TIMESTAMP=$(date +%s%N)

# Email settings (SES)
EMAIL_TO="${EMAIL_TO:-lalithmuppalla123@gmail.com}"
EMAIL_SUBJECT="${EMAIL_SUBJECT:-Nimbus Test Email}"
EMAIL_BODY="${EMAIL_BODY:-Hello from Nimbus SES! (sent at $TIMESTAMP)}"

# SMS settings (SNS)
SMS_PHONE="${SMS_PHONE:-+13464817137}"
SMS_MESSAGE="${SMS_MESSAGE:-Nimbus SMS test [$TIMESTAMP]: your code is 123456}"

# Webhook settings
WEBHOOK_URL="${WEBHOOK_URL:-https://webhook.site/d53b4314-d8cf-4f4f-868d-7a45726d7326}"
WEBHOOK_METHOD="${WEBHOOK_METHOD:-POST}"
WEBHOOK_TIMEOUT_SEC="${WEBHOOK_TIMEOUT_SEC:-30}"

function post_json() {
  local payload=$1
  curl -sS -X POST "${BASE_URL}/v1/notifications" \
    -H "Content-Type: application/json" \
    -d "$payload" | jq . 2>/dev/null || echo "$payload" | jq .
  echo
}

echo "==> Sending EMAIL notification..."
EMAIL_PAYLOAD=$(cat <<EOF
{
  "tenant_id": "00000000-0000-0000-0000-000000000001",
  "user_id": "00000000-0000-0000-0000-000000000002",
  "channel": "email",
  "payload": {
    "to": "${EMAIL_TO}",
    "subject": "Nimbus Test Email",
    "body": "Hello from Nimbus SES! [$TIMESTAMP]"
  }
}
EOF
)
post_json "$EMAIL_PAYLOAD"

echo "==> Sending SMS notification..."
SMS_PAYLOAD=$(cat <<EOF
{
  "tenant_id": "00000000-0000-0000-0000-000000000001",
  "user_id": "00000000-0000-0000-0000-000000000002",
  "channel": "sms",
  "payload": {
    "phone_number": "${SMS_PHONE}",
    "message": "Nimbus SMS test [$TIMESTAMP]: your code is 123456"
  }
}
EOF
)
post_json "$SMS_PAYLOAD"

echo "==> Sending WEBHOOK notification..."
WEBHOOK_PAYLOAD=$(cat <<EOF
{
  "tenant_id": "00000000-0000-0000-0000-000000000001",
  "user_id": "00000000-0000-0000-0000-000000000002",
  "channel": "webhook",
  "payload": {
    "url": "https://webhook.site/d53b4314-d8cf-4f4f-868d-7a45726d7326",
    "method": "POST",
    "headers": {
      "X-Test": "nimbus"
    },
    "body": {
      "event": "test_event",
      "message": "hello webhook [$TIMESTAMP]"
    },
    "timeout_sec": 30
  }
}
EOF
)
post_json "$WEBHOOK_PAYLOAD"

echo "==> Done. Check logs and /v1/dlq if any failures."