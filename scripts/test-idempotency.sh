#!/bin/bash

ALB="http://nimbus-prod-1445650077.us-east-1.elb.amazonaws.com"
IDEMP_KEY="test-$(date +%s)"

echo "Using Idempotency-Key: $IDEMP_KEY"
echo ""

TENANT="550e8400-e29b-41d4-a716-446655440001"
USER="550e8400-e29b-41d4-a716-446655440002"

echo "=== First Request ==="
RESP1=$(curl -s -X POST "$ALB/v1/notifications" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $IDEMP_KEY" \
  -d "{\"tenant_id\":\"$TENANT\",\"user_id\":\"$USER\",\"channel\":\"email\",\"payload\":{\"to\":\"lalithmuppalla123@gmail.com\",\"subject\":\"Idempotency Test\",\"body\":\"Should only send once!\"}}")

echo "Response: $RESP1"
echo ""

sleep 2

echo "=== Second Request (same key - should be replayed) ==="
RESP2=$(curl -s -i -X POST "$ALB/v1/notifications" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $IDEMP_KEY" \
  -d "{\"tenant_id\":\"$TENANT\",\"user_id\":\"$USER\",\"channel\":\"email\",\"payload\":{\"to\":\"lalithmuppalla123@gmail.com\",\"subject\":\"Idempotency Test\",\"body\":\"Should only send once!\"}}")

echo "$RESP2"
echo ""

echo "=== Third Request (NO idempotency key - new notification) ==="
RESP3=$(curl -s -X POST "$ALB/v1/notifications" \
  -H "Content-Type: application/json" \
  -d "{\"tenant_id\":\"$TENANT\",\"user_id\":\"$USER\",\"channel\":\"email\",\"payload\":{\"to\":\"lalithmuppalla123@gmail.com\",\"subject\":\"No Idempotency Key\",\"body\":\"This WILL send a new email\"}}")

echo "Response: $RESP3"
