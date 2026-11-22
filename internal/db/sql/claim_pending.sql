WITH candidates AS MATERIALIZED (
    SELECT id
    FROM notifications
    WHERE status IN ('pending', 'processing')
      AND nimbus_claimable_at(status, created_at, next_retry_at, updated_at) <= now() AT TIME ZONE 'UTC'
    ORDER BY nimbus_claimable_at(status, created_at, next_retry_at, updated_at), created_at, id
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
UPDATE notifications AS n
SET status = 'processing'
FROM candidates AS c
WHERE n.id = c.id
RETURNING n.id, n.tenant_id, n.user_id, n.channel, n.payload,
          n.status, n.attempt, n.error_message, n.next_retry_at, n.created_at, n.updated_at
